package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"os/exec"
	"strings"

	maxbot "github.com/max-messenger/max-bot-api-client-go"
	"github.com/max-messenger/max-bot-api-client-go/schemes"
	"github.com/hekmon/transmissionrpc/v3"
	"github.com/spf13/viper"
)

type Config struct {
    Token       string `mapstructure:"max_token"`
    AllowedUIDs []int64 `mapstructure:"allowed_uids"`
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
	Api *maxbot.Api
	Client *transmissionrpc.Client
	Cfg Config
}

func NewAppState(ctx context.Context) *appState {
	os.MkdirAll("./downloads", 0755)
	cfg := parseConfig()
	api, err := maxbot.New(cfg.Token)
	if err != nil {
		panic(err)
	}

	// bot.Debug = true
	
	transmissionClient := NewTransmissionClient(cfg.Transmission.Username, cfg.Transmission.Password, cfg.Transmission.Host, cfg.Transmission.Scheme, cfg.Transmission.Port)
	return &appState{api, transmissionClient, cfg}
}

func downloadFile(url string, fileID string, chatID int64, cfg Config) error {
	cmd := exec.Command("curl", url, "-o", "./downloads/" + fileID + ".torrent")
	err := cmd.Run()
	if err != nil {
		return err
	}
	return nil
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
        exit := make(chan os.Signal)
        signal.Notify(exit, os.Kill, os.Interrupt)
        <-exit
        cancel()
    }()
	state := NewAppState(ctx)


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
			switch upd.GetCommand() {
			case "/cleanup":
				cnt, err := Cleanup(ctx, state.Client)
				if err != nil {
					slog.Error("Error sending cleanup", "error", err)
					state.Api.Messages.Send(ctx, maxbot.NewMessage().SetChat(message.Recipient.ChatId).SetText( fmt.Sprintf("Error sending cleanup: %v", err)))
				}
				var message string
				if cnt == 0 {
					message = "No active torrents"
				} else {
					message = fmt.Sprintf("Active torrents removed: %d", cnt)
				}
				state.Api.Messages.Send(ctx, maxbot.NewMessage().SetChat(upd.Message.Recipient.ChatId).SetText(message))
				continue
			case "/status":
				active, downloaded, err := StatusOfActiveTorrents(ctx, state.Client)
				if err != nil {
					slog.Error("Error sending status", "error", err)
					state.Api.Messages.Send(ctx, maxbot.NewMessage().SetChat(message.Recipient.ChatId).SetText(fmt.Sprintf("Error sending status: %v", err)))
					continue
				}
				var messageToSend string
				if active == 0 {
					messageToSend = "No active torrents."
				} else {
					messageToSend = fmt.Sprintf("Number of active torrents: %d.", active)
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
					err := processFileAttachment(ctx, fileAttachment, state, message)
					slog.Error("Error downloading file", "error", err)
				}
			}
            default:
				slog.Debug("Update", "update", upd)
			
        }
    }
}

func processFileAttachment(ctx context.Context, attachment *schemes.FileAttachment, state *appState, message schemes.Message) error {
	fileID := attachment.Filename
	url := attachment.Payload.Url
	err := downloadFile(url, fileID, message.Recipient.ChatId, state.Cfg)
	if err != nil {
		slog.Error("Error downloading file", "error", err)
		state.Api.Messages.Send(ctx, maxbot.NewMessage().SetChat(message.Recipient.ChatId).SetText(fmt.Sprintf("Error downloading file: %v", err)))
		return err
	}
	err = SendFileToTransmission(state.Client, fileID, message.Recipient.ChatId)
	if err != nil {
		slog.Error("Error sending file to transmission", "error", err)
		state.Api.Messages.Send(ctx, maxbot.NewMessage().SetChat(message.Recipient.ChatId).SetText("Error sending file to transmission"))
		return err
	}
	state.Api.Messages.Send(ctx, maxbot.NewMessage().SetChat(message.Recipient.ChatId).SetText("File sent to transmission"))
	return nil
}
