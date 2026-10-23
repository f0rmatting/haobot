package service

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/f0rmatting/haobot/internal/agent/modem"
)

// ErrLastAttempt means the operation would use the SIM's last PIN attempt;
// callers must confirm explicitly (force) to go ahead.
var ErrLastAttempt = errors.New("只剩最后 1 次 PIN 尝试机会，输错将锁卡，需要 PUK 才能解锁")

// friendly turns modem error results into messages for people.
func friendly(err error) error {
	var cmdErr *modem.CommandError
	if !errors.As(err, &cmdErr) {
		return err
	}
	result := strings.ToLower(cmdErr.Result)
	switch {
	case strings.Contains(result, "incorrect password") || strings.HasSuffix(result, " 16"):
		return errors.New("密码错误，已消耗一次尝试机会")
	case strings.Contains(result, "puk required") || strings.HasSuffix(result, " 12"):
		return errors.New("SIM 卡已被锁定，需要 PUK 码解锁")
	case strings.Contains(result, "not inserted") || strings.HasSuffix(result, " 10"):
		return errors.New("未检测到 SIM 卡")
	case strings.Contains(result, "not allowed") || strings.HasSuffix(result, " 3"):
		return errors.New("模组不允许此操作（当前状态下不支持）")
	case strings.Contains(result, "not supported") || strings.HasSuffix(result, " 4"):
		return errors.New("模组不支持此操作")
	}
	return fmt.Errorf("模组返回错误：%s", cmdErr.Result)
}

// guardPIN refuses to spend the last PIN attempt without force.
func guardPIN(ctx context.Context, m *modem.Modem, force bool) error {
	left, _, err := m.PINRetries(ctx)
	if err == nil && left <= 1 && !force {
		return ErrLastAttempt
	}
	return nil
}

// EnterPIN unlocks a SIM that is waiting for its PIN.
func (s *Service) EnterPIN(ctx context.Context, pin string, force bool) error {
	return s.Do(ctx, func(ctx context.Context, m *modem.Modem) error {
		state, err := m.SIMState(ctx)
		if err != nil {
			return friendly(err)
		}
		if state != modem.SIMPIN {
			return fmt.Errorf("SIM 卡当前不需要 PIN（状态：%s）", state)
		}
		if err := guardPIN(ctx, m, force); err != nil {
			return err
		}
		if err := m.EnterPIN(ctx, pin); err != nil {
			s.note(ctx, "warn", "手动输入 SIM PIN 失败")
			return friendly(err)
		}
		s.pinBlocked.Store(false)
		s.note(ctx, "info", "SIM 卡已解锁（手动输入 PIN）")
		return nil
	})
}

// EnterPUK unblocks a PUK-locked SIM and sets a new PIN.
func (s *Service) EnterPUK(ctx context.Context, puk, newPIN string) error {
	return s.Do(ctx, func(ctx context.Context, m *modem.Modem) error {
		state, err := m.SIMState(ctx)
		if err != nil {
			return friendly(err)
		}
		if state != modem.SIMPUK {
			return fmt.Errorf("SIM 卡当前不需要 PUK（状态：%s）", state)
		}
		if err := m.EnterPUK(ctx, puk, newPIN); err != nil {
			s.note(ctx, "warn", "输入 PUK 失败")
			return friendly(err)
		}
		s.pinBlocked.Store(false)
		s.note(ctx, "info", "SIM 卡已用 PUK 解锁并设置了新 PIN")
		return nil
	})
}

// SetPINLock turns the power-up PIN requirement on or off.
func (s *Service) SetPINLock(ctx context.Context, enabled bool, pin string, force bool) error {
	return s.Do(ctx, func(ctx context.Context, m *modem.Modem) error {
		if err := guardPIN(ctx, m, force); err != nil {
			return err
		}
		if err := m.SetPINLock(ctx, enabled, pin); err != nil {
			return friendly(err)
		}
		if enabled {
			s.note(ctx, "info", "已开启 SIM PIN 锁")
		} else {
			s.note(ctx, "info", "已关闭 SIM PIN 锁")
		}
		return nil
	})
}

// ChangePIN replaces the SIM PIN.
func (s *Service) ChangePIN(ctx context.Context, oldPIN, newPIN string, force bool) error {
	return s.Do(ctx, func(ctx context.Context, m *modem.Modem) error {
		if err := guardPIN(ctx, m, force); err != nil {
			return err
		}
		if err := m.ChangePIN(ctx, oldPIN, newPIN); err != nil {
			return friendly(err)
		}
		s.note(ctx, "info", "已修改 SIM PIN")
		return nil
	})
}

// ResumeAutoUnlock re-enables automatic unlocking after the stored PIN was
// changed.
func (s *Service) ResumeAutoUnlock() {
	s.pinBlocked.Store(false)
	s.update(func(*Snapshot) {})
}

// Reboot restarts the module; the connection comes back on its own.
func (s *Service) Reboot(ctx context.Context) error {
	return s.Do(ctx, func(ctx context.Context, m *modem.Modem) error {
		s.note(ctx, "info", "正在重启模组")
		return friendly(m.Reboot(ctx))
	})
}

// SetRadio switches between normal operation and airplane mode.
func (s *Service) SetRadio(ctx context.Context, on bool) error {
	fun, text := modem.FunAirplane, "已开启飞行模式（射频关闭，收不到短信）"
	if on {
		fun, text = modem.FunFull, "已关闭飞行模式"
	}
	return s.Do(ctx, func(ctx context.Context, m *modem.Modem) error {
		if err := m.SetFunctionality(ctx, fun); err != nil {
			return friendly(err)
		}
		s.note(ctx, "info", text)
		return nil
	})
}

// SetScanMode limits the radio technologies the module may use.
func (s *Service) SetScanMode(ctx context.Context, mode int) error {
	return s.Do(ctx, func(ctx context.Context, m *modem.Modem) error {
		if err := m.SetScanMode(ctx, mode); err != nil {
			return friendly(err)
		}
		s.note(ctx, "info", "网络制式已设为："+modem.ScanModes[mode])
		return nil
	})
}

// Console runs a command typed in the AT console.
func (s *Service) Console(ctx context.Context, cmd string) ([]string, error) {
	cmd = strings.TrimSpace(cmd)
	if err := modem.CheckConsole(cmd); err != nil {
		return nil, err
	}
	var lines []string
	err := s.Do(ctx, func(ctx context.Context, m *modem.Modem) error {
		s.note(ctx, "info", "AT 控制台执行："+modem.Redact(cmd))
		var err error
		lines, err = m.Raw(ctx, cmd, modem.ConsoleTimeout)
		return err
	})
	return lines, err
}

// Refresh re-reads the modem status now instead of at the next poll.
func (s *Service) Refresh(ctx context.Context) error {
	return s.Do(ctx, func(context.Context, *modem.Modem) error { return nil })
}
