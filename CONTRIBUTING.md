# 参与贡献

感谢你愿意改进号小助！

## 报告问题

模组相关的问题，跟型号、固件和运营商关系很大。提 Issue 时请附上：

- `haobot-agent version` 的输出，操作系统和 CPU 架构
- 模组型号和固件版本：服务停止时运行 `haobot-agent at ATI 'AT+QGMR'`
- 运营商，以及是否开通了 VoLTE
- “日志”页里相关的记录（请先删掉手机号、短信内容等个人信息）

## 开发环境

- Go（版本见 `go.mod`）
- macOS 需要 `brew install libusb pkg-config`；Linux 用 libusb 方式时需要 `libusb-1.0-0-dev`
- 改前端需要 Node 22 和 pnpm（版本见 `ui/package.json` 的 `packageManager`）

```sh
make test          # Go 测试
make build         # 编译到 bin/
make ui            # 编译前端到 internal/agent/web/dist
make ui-dev        # 前端热更新，/api 代理到本机 9081 端口的 Agent
```

没有模组也能开发大部分功能：测试里用假的模组（`net.Pipe` 上的 AT 应答）和假的大模型、推送服务器。

## 发布新版本（维护者）

1. 更新 `TODO.md` 里已完成的项目。
2. 打标签并推送：`git tag v1.2.3 && git push origin v1.2.3`。
3. GitHub Actions 会测试、编译 5 个平台并创建 Release。本地想先试一下打包，可以运行 `make dist VERSION=v1.2.3`。

## 提交 Pull Request

- 提交前运行 `gofmt`、`go vet ./...` 和 `go test ./...`。改了 `ui/` 的话，运行 `make ui` 并把 `internal/agent/web/dist` 一起提交（这样只改 Go 的人不需要 Node）。CI 会检查这些。
- 新功能和修复请带上测试，尤其是 AT 指令解析、短信编解码和通话状态这类在真机上难以复现的逻辑。
- 代码风格跟周围保持一致：注释说明“为什么”，界面文字用简体中文。
- **不要提交任何真实数据**：数据库、`secret.key`、证书、录音、备份文件，以及真实的手机号、IMEI、API Key。测试里用 `13800138000` 这类示例号码。

## 许可

提交的代码按 [MIT 许可证](LICENSE) 发布。
