package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"

	maxbot "github.com/max-messenger/max-bot-api-client-go"
	"github.com/max-messenger/max-bot-api-client-go/schemes"
	"github.com/spf13/viper"
)

type Config struct {
	Token        string  `mapstructure:"max_token"`
	AllowedUIDs  []int64 `mapstructure:"allowed_uids"`
	Transmission struct {
		Username string `mapstructure:"username"`
		Password string `mapstructure:"password"`
		Host     string `mapstructure:"host"`
		Port     int    `mapstructure:"port"`
		Scheme   string `mapstructure:"scheme"`
	} `mapstructure:"transmission"`
}

func parseConfig() Config {
	v := viper.New()
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))

	// Set defaults
	v.SetDefault("max_token", "")
	v.SetDefault("allowed_uids", []int64{})
	v.SetDefault("transmission.username", "transmission")
	v.SetDefault("transmission.password", "")
	v.SetDefault("transmission.host", "127.0.0.1")
	v.SetDefault("transmission.port", 9091)

	// Config file
	v.SetConfigName("config")
	v.SetConfigType("yaml")
	v.AddConfigPath(".")
	v.AddConfigPath("/etc/transmissionbot/")

	// Environment variables
	v.AutomaticEnv()

	// Read config
	if err := v.ReadInConfig(); err != nil {
		if !strings.Contains(err.Error(), "Config File Not Found") {
			panic(err)
		}
		// Config file not found; ignore, we'll rely on defaults/env
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		panic(err)
	}

	// Validate required fields
	if cfg.Token == "" {
		panic("max_token is required")
	}
	if cfg.Transmission.Password == "" {
		panic("transmission_password is required")
	}
	if cfg.Transmission.Scheme == "" {
		panic("transmission_scheme is required")
	}

	return cfg
}

func invalidUid(uid int64, allowed []int64) bool {
	for _, allowedUID := range allowed {
		if uid == allowedUID {
			return false
		}
	}
	return true
}

type appState struct {
	Api    *maxbot.Api
	Client *TransmissionClient
	Cfg    Config

	// lastChatID remembers the most recent chat an allowed user sent a message
	// from, so background notifications (e.g. cleanup) know where to go.
	chatMu     sync.Mutex
	lastChatID int64

	cleanup *cleanupRunner
}

// rememberChat stores the chat ID of an allowed user's message.
func (s *appState) rememberChat(chatID int64) {
	s.chatMu.Lock()
	s.lastChatID = chatID
	s.chatMu.Unlock()
}

// CurChatID returns the most recent allowed-user chat ID (0 if none known yet).
func (s *appState) CurChatID() int64 {
	s.chatMu.Lock()
	defer s.chatMu.Unlock()
	return s.lastChatID
}

func NewAppState(ctx context.Context) *appState {
	if err := os.MkdirAll("./downloads", 0755); err != nil {
		panic(fmt.Sprintf("cannot create downloads directory: %v", err))
	}
	cfg := parseConfig()
	api, err := maxbot.New(cfg.Token)
	if err != nil {
		panic(err)
	}

	// bot.Debug = true

	transmissionClient := NewTransmissionClient(cfg.Transmission.Username, cfg.Transmission.Password, cfg.Transmission.Host, cfg.Transmission.Scheme, cfg.Transmission.Port)
	// Fail fast if Transmission is unreachable at startup.
	if err := transmissionClient.Ping(ctx); err != nil {
		slog.Error("Cannot reach Transmission", "error", err)
		panic(fmt.Sprintf("cannot reach transmission: %v", err))
	}
	state := &appState{Api: api, Client: transmissionClient, Cfg: cfg}
	state.cleanup = newCleanupRunner(transmissionClient, api, state.CurChatID)
	return state
}

func downloadFile(ctx context.Context, url string, fileID string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download returned status %d", resp.StatusCode)
	}

	out, err := os.Create(filepath.Join("./downloads", fileID+".torrent"))
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, resp.Body)
	return err
}

func main() {
	// Setup logger
	logLevel := slog.LevelDebug
	if level := os.Getenv("LOG_LEVEL"); level == "info" {
		logLevel = slog.LevelInfo
	} else if level == "warn" {
		logLevel = slog.LevelWarn
	} else if level == "error" {
		logLevel = slog.LevelError
	}
	handler := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: logLevel,
	})
	slog.SetDefault(slog.New(handler))

	ctx, cancel := context.WithCancel(context.Background()) // создам
	go func() {
		exit := make(chan os.Signal, 1)
		signal.Notify(exit, os.Kill, os.Interrupt)
		<-exit
		cancel()
	}()
	state := NewAppState(ctx)
	state.cleanup.Start(ctx)
	slog.Info("Started periodic cleanup goroutine", "interval", cleanupInterval)

	for upd := range state.Api.GetUpdates(ctx) {
		slog.Debug("Update", "update", upd)

		switch upd := upd.(type) { // Определение типа пришедшего обновления

		case *schemes.BotStartedUpdate:
			state.Api.Messages.Send(ctx, maxbot.NewMessage().SetChat(upd.ChatId).SetText("Привет! Бот запущен"))
		case *schemes.MessageCreatedUpdate:
			message := upd.Message
			if invalidUid(message.Sender.UserId, state.Cfg.AllowedUIDs) {
				slog.Warn("Invalid UID", "uid", message.Sender.UserId)
				continue
			}
			state.rememberChat(message.Recipient.ChatId)
			switch upd.GetCommand() {
			case "/status":
				restarted := state.cleanup.Fix(ctx)
				active, downloaded, err := StatusOfActiveTorrents(ctx, state.Client)
				if err != nil {
					slog.Error("Error sending status", "error", err)
					state.Api.Messages.Send(ctx, maxbot.NewMessage().SetChat(message.Recipient.ChatId).SetText(fmt.Sprintf("Error sending status: %v", err)))
					continue
				}
				var messageToSend string
				if restarted {
					messageToSend = "⚠️ Background cleanup had stopped and was restarted.\n"
				}
				if active == 0 {
					messageToSend += "No active torrents."
				} else {
					messageToSend += fmt.Sprintf("Number of active torrents: %d.", active)
				}
				if downloaded == 0 {
					messageToSend += " No finished torrents."
				} else {
					messageToSend += fmt.Sprintf(" Number of finished torrents: %d.", downloaded)
				}
				state.Api.Messages.Send(ctx, maxbot.NewMessage().SetChat(message.Recipient.ChatId).SetText(messageToSend))
				continue
			}
			for _, attachment := range message.Body.Attachments {
				if fileAttachment, ok := attachment.(*schemes.FileAttachment); ok {
					if err := processFileAttachment(ctx, fileAttachment, state, message); err != nil {
						// processFileAttachment already logs the specific error.
						continue
					}
				}
			}
		default:
			slog.Debug("Update", "update", upd)

		}
	}
}

func processFileAttachment(ctx context.Context, attachment *schemes.FileAttachment, state *appState, message schemes.Message) error {
	// Use the message's unique ID (not the user-supplied filename) to avoid
	// collisions and path-traversal via malicious filenames.
	fileID := message.Body.Mid
	url := attachment.Payload.Url
	err := downloadFile(ctx, url, fileID)
	if err != nil {
		slog.Error("Error downloading file", "error", err)
		state.Api.Messages.Send(ctx, maxbot.NewMessage().SetChat(message.Recipient.ChatId).SetText(fmt.Sprintf("Error downloading file: %v", err)))
		return err
	}
	err = SendFileToTransmission(state.Client, fileID)
	if err != nil {
		slog.Error("Error sending file to transmission", "error", err)
		state.Api.Messages.Send(ctx, maxbot.NewMessage().SetChat(message.Recipient.ChatId).SetText("Error sending file to transmission"))
		return err
	}
	state.Api.Messages.Send(ctx, maxbot.NewMessage().SetChat(message.Recipient.ChatId).SetText("File sent to transmission"))
	return nil
}
