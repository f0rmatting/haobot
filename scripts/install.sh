#!/bin/sh
# Installs or upgrades 号小助 (haobot) on Linux or macOS: downloads the
# release for this computer, checks it against SHA256SUMS and puts
# haobot-agent on the PATH. Nothing is set to start with the computer; the
# agent runs when you say "haobot-agent start".
#
#   curl -fsSL https://raw.githubusercontent.com/f0rmatting/haobot/main/scripts/install.sh | sh
#
# HAOBOT_VERSION=v1.2.3 installs that release instead of the latest.
# HAOBOT_MIRROR=https://mirror.example/ is put before the github.com
# download links, for networks where GitHub is slow.
# HAOBOT_BIN=/some/dir installs there instead of /usr/local/bin (when
# writable) or ~/.local/bin.
set -eu

repo=f0rmatting/haobot
mirror=${HAOBOT_MIRROR:-}

say() { printf '%s\n' "$*" >&2; }
fail() {
	say "错误：$*"
	exit 1
}

case $(uname -s) in
Linux) os=linux ;;
Darwin) os=darwin ;;
*) fail "只支持 Linux 和 macOS，当前系统是 $(uname -s)" ;;
esac
case $(uname -m) in
x86_64 | amd64) arch=amd64 ;;
aarch64 | arm64) arch=arm64 ;;
armv7*) arch=armv7 ;;
*) fail "不支持的 CPU 架构 $(uname -m)，可以按 README 的“编译”自己编译" ;;
esac
command -v curl >/dev/null || fail "需要 curl"
command -v tar >/dev/null || fail "需要 tar"

version=${HAOBOT_VERSION:-}
if [ -z "$version" ]; then
	# The latest release page redirects to its tag.
	version=$(curl -fsSLI -o /dev/null -w '%{url_effective}' "https://github.com/$repo/releases/latest" | sed 's#.*/tag/##')
	case $version in
	v*) ;;
	*) fail "查不到最新版本（网络连不上 GitHub？），可以用 HAOBOT_VERSION=v1.2.3 指定版本" ;;
	esac
fi

name=haobot-agent-$version-$os-$arch
base=${mirror}https://github.com/$repo/releases/download/$version
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

say "下载号小助 ${version}（$os-${arch}）…"
curl -fL --progress-bar -o "$tmp/$name.tar.gz" "$base/$name.tar.gz" || fail "下载失败：$base/$name.tar.gz"
curl -fsSL -o "$tmp/SHA256SUMS" "$base/SHA256SUMS" || fail "下载校验文件失败"
(
	cd "$tmp"
	grep " $name.tar.gz\$" SHA256SUMS >sum || fail "SHA256SUMS 里没有 $name.tar.gz"
	if command -v sha256sum >/dev/null; then
		sha256sum -c sum >/dev/null
	else
		shasum -a 256 -c sum >/dev/null
	fi
) || fail "校验失败，下载的文件不完整或被改动过"
tar -xzf "$tmp/$name.tar.gz" -C "$tmp"
agent=$tmp/$name/haobot-agent

# Where to put it: a directory already on the PATH when we may write there.
dir=${HAOBOT_BIN:-}
if [ -z "$dir" ]; then
	if [ -w /usr/local/bin ]; then
		dir=/usr/local/bin
	else
		dir=$HOME/.local/bin
	fi
fi
mkdir -p "$dir"
dst=$dir/haobot-agent

# An agent running in the background is stopped for the upgrade and started
# again with the options it remembers.
was_running=
if [ -x "$dst" ] && "$dst" status >/dev/null 2>&1; then
	was_running=1
	"$dst" stop
fi
cp "$agent" "$dst.new"
chmod 755 "$dst.new"
mv -f "$dst.new" "$dst"
say "已安装到 ${dst}（$("$dst" version)）"

# Questions are asked on the terminal, since stdin is this script.
tty=
if [ -t 1 ] && (: </dev/tty) 2>/dev/null; then
	tty=1
fi
ask() { # ask "question" default(y|n)
	[ -n "$tty" ] || { [ "$2" = y ]; return; }
	printf '%s ' "$1" >&2
	read -r answer </dev/tty || answer=
	case ${answer:-$2} in
	[yY]*) return 0 ;;
	*) return 1 ;;
	esac
}

case ":$PATH:" in
*":$dir:"*) cmd=haobot-agent ;;
*)
	cmd=$dst
	say ""
	say "提示：$dir 不在 PATH 里。把下面这行加到 ~/.profile（zsh 是 ~/.zshrc）后重新打开终端，就能直接用 haobot-agent："
	say "  export PATH=\"$dir:\$PATH\""
	;;
esac

if [ "$os" = linux ] && [ "$(id -u)" != 0 ] && ! id -nG | grep -qwE 'dialout|uucp'; then
	say ""
	say "提示：当前用户没有读写模组串口的权限。执行下面的命令后注销再登录（或重启）："
	say "  sudo usermod -aG $(getent group uucp >/dev/null 2>&1 && ! getent group dialout >/dev/null 2>&1 && echo uucp || echo dialout) $USER"
	say "模组被 ModemManager 占用、或者想让卡死时自动复位模组，再装上 udev 规则："
	say "  $cmd udev-rules | sudo tee /etc/udev/rules.d/99-haobot.rules && sudo udevadm control --reload && sudo udevadm trigger"
fi

say ""
if [ -n "$was_running" ]; then
	"$dst" start
elif ask "现在启动号小助吗？[Y/n]" y; then
	if ask "允许局域网里的其他电脑、手机打开管理后台吗？（NAS、树莓派选 y）[y/N]" n; then
		"$dst" start -lan
	else
		"$dst" start
	fi
else
	say "以后启动：$cmd start（加 -lan 允许局域网访问），停止：$cmd stop"
fi
