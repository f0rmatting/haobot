# haobot (号小助)

[中文](README.md)

haobot watches the phone number in a cellular modem (Quectel EC20; other Quectel and SIMCom modules are recognised but untested) plugged into a computer you keep running. It:

- receives SMS and sorts them with an LLM (verification codes, bills, deliveries, marketing, spam, carrier, notices, personal). Messages are redacted locally before they are sent to the model;
- lets an AI answer calls you miss. It asks who is calling and why, then saves a summary. It uses Alibaba Cloud Bailian (Model Studio) realtime speech models;
- pushes messages, missed calls and call summaries to Telegram, Feishu (Lark), DingTalk or WeCom;
- serves a web admin UI for conversations, replies, calls through the modem's SIM (browser microphone), device health and settings.

It is a single Go binary for macOS and Linux (including NAS boxes) with an embedded React UI and a local SQLite database. The UI and most documentation are in Chinese, since the project targets mainland Chinese carriers.

> **Before you use it**
>
> - Use it only on your own number. Do not use it for bulk messaging or to intercept messages that are not yours.
> - **Call recording**: many jurisdictions require telling the other party. By default only calls the AI answers are recorded. You can turn recording off.
> - **AI answering**: by default the assistant does not announce that it is an AI, but it answers truthfully when sincerely asked. Where the law requires disclosure, enable "接通后主动说明自己是 AI" in the AI answering settings.
> - **Where data goes**:
>   - redacted SMS go to the LLM you configure;
>   - call audio is streamed to Bailian while the AI answers;
>   - pushed content passes through the push platform.
>
>   Everything else stays on your machine.
> - MIT licensed, without warranty. You bear any costs (SMS, calls, LLM usage) and consequences of using it.

## Quick start

1. Download a binary from [Releases](https://github.com/f0rmatting/haobot/releases), install it with `go install github.com/f0rmatting/haobot/cmd/haobot-agent@latest` (on macOS run `brew install libusb pkg-config` first), or build it:

   ```sh
   brew install libusb pkg-config   # macOS only
   make build                       # bin/haobot-agent
   make linux                       # static Linux amd64/arm64 binaries (serial transport)
   ```

2. Plug in the modem and run `./haobot-agent serve`. Add `-listen 0.0.0.0` to allow LAN access.
3. The first start prints a generated password for the user `admin`. Open <https://127.0.0.1:9081>. The certificate is self-signed: compare its SHA-256 with the `sha256=` line in the log, then accept it.
4. In 设置 (Settings), configure classification (智能分类), push channels (消息推送) and AI answering (AI 接听). Each section has a test button.

Other commands:

| Command | What it does |
|---|---|
| `haobot-agent passwd` | Change the admin password |
| `haobot-agent backup` / `restore` | Move your data to another machine |
| `haobot-agent at 'ATI'` | Send raw AT commands while the service is stopped |
| `haobot-agent version` | Print the version |

See [README.md](README.md) for the full documentation: flags, health checks, calls, AI answering, push rules, HTTPS and security.

## Contributing

Issues and pull requests are welcome; see [CONTRIBUTING.md](CONTRIBUTING.md). Report security problems privately as described in [SECURITY.md](SECURITY.md).

## License

[MIT](LICENSE)
