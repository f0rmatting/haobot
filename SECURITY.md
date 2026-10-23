# 安全策略 / Security Policy

号小助管理着你的短信、通话录音和各种密钥，安全问题我们会优先处理。

## 报告漏洞

**请不要在公开 Issue 里报告安全问题。** 请使用 GitHub 的私密漏洞报告：仓库页面 → Security → Report a vulnerability。

报告里请尽量写清楚：

- 受影响的版本（`haobot-agent version`）和运行环境
- 复现步骤或概念验证
- 可能的影响（例如：未登录可以读取短信）

我们会在 7 天内回复，修复后在发布说明里致谢（除非你希望匿名）。

## 范围

特别关注：

- 管理后台的认证、会话、跨站请求（CSRF）和跨站脚本（XSS）
- 加密保存的 SIM PIN、API Key、推送密钥被读出
- 备份和恢复（`backup` / `restore`）的路径穿越
- 来电者通过语音让 AI 接听助手泄露信息或执行越权操作（提示词注入）

不在范围内：需要先拿到运行 Agent 的机器的系统权限才能做到的事；把管理后台不加防护地暴露在公网上导致的问题（请看 README 的“安全”一节）。

## Reporting (English)

Please do not open public issues for security problems. Use GitHub's private vulnerability reporting (Security → Report a vulnerability) and include the version, steps to reproduce and the impact. We aim to respond within 7 days.
