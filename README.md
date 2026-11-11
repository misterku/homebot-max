# TransmissionBot for MAX – MAX Bot for Transmission

TransmissionBot is a lightweight MAX bot that allows you to manage your Transmission torrent client remotely. It provides a simple interface for uploading `.torrent` files, cleaning up finished downloads, and checking the status of active torrents—all from within MAX.

## Features

- **Torrent Upload**: Send a `.torrent` file to the bot, and it will automatically add it to Transmission.
- **Cleanup**: Automatically removes finished torrents from Transmission in the background (every 10 minutes); you're notified when something is removed.
- **Status Check**: Get a quick overview of active and finished torrents with `/status`.
- **Access Control**: Only authorized users (by MAX User ID) can interact with the bot.

## Technologies

- **Go** (1.24+) – Core programming language.
- **MAX Bot API** – via `max-bot-api-client-go`.
- **Transmission RPC** – via a built-in HTTP client (no external library).
- **Configuration** – `viper` for YAML/ENV configuration.

## Configuration

1. Copy `config.example.yaml` to `config.yaml`.
2. Fill in your MAX bot token (obtained from MAX bot platform).
3. Set your Transmission RPC credentials (username, password, host, scheme, port).
4. Specify allowed MAX User IDs in `allowed_uids`.

Example `config.yaml`:

```yaml
max_token: "YOUR_MAX_BOT_TOKEN"
allowed_uids: [123456789]

transmission:
  username: "transmission"
  password: "YOUR_TRANSMISSION_PASSWORD"
  host: "127.0.0.1"
  scheme: "http"
  port: 9091
```

Environment variables are also supported (e.g., `MAX_TOKEN`, `TRANSMISSION_PASSWORD`).

## Usage

1. Build the bot:
   ```bash
   go build
   ```
2. Run the executable:
   ```bash
   ./maxbot
   ```
3. Send a `.torrent` file to your bot in MAX (if your UID is allowed).
4. Use the commands:
   - `/status` – show active/finished torrent counts and verify the background cleanup task is running.

## Development & Vibe‑Coding

This project was developed using **Visual Studio Code** with the **SourceCraft Code Assistant** extension, which provided intelligent code completion, refactoring, and real‑time feedback—making the development flow smooth and enjoyable.

### Logging

All output now uses structured logging via `log/slog`. Log levels are:
- `DEBUG`: Invalid updates, general update info.
- `WARN`: Unauthorized user attempts.
- `ERROR`: Download/transmission errors, cleanup/status failures.

By default, the logger outputs **DEBUG** and higher levels (so all previous `fmt.Print` messages remain visible). You can raise the minimum level by setting the `LOG_LEVEL` environment variable to `info`, `warn`, or `error` (e.g., `LOG_LEVEL=info`).

## License

MIT
