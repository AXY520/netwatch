package probe

import (
	"context"
	"errors"
	"strings"

	"netwatch/internal/logger"
	"netwatch/internal/lzcsdk"
)

// IPv6RenewNIC 是一块可执行 IPv6 续约的网卡(透传给前端选择)。
type IPv6RenewNIC struct {
	Device     string `json:"device"`
	Type       string `json:"type"`
	State      string `json:"state"`
	Connection string `json:"connection,omitempty"`
}

// IPv6RenewResult 是续约操作的结果。
type IPv6RenewResult struct {
	OK     bool   `json:"ok"`
	Device string `json:"device,omitempty"`
	Output string `json:"output,omitempty"`
	Error  string `json:"error,omitempty"`
}

// ListIPv6RenewNICs 列出当前可续约的网卡(经 lzc-sdk 调用系统 nmcli)。
// SDK 不可用(非懒猫环境)时返回 error,前端据此隐藏功能。
func (s *Service) ListIPv6RenewNICs(ctx context.Context) ([]IPv6RenewNIC, error) {
	if !lzcsdk.Available() {
		return nil, errors.New("lzc-sdk 不可用(非懒猫环境)")
	}
	devs, err := lzcsdk.ListReapplicableNICs(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]IPv6RenewNIC, 0, len(devs))
	for _, d := range devs {
		out = append(out, IPv6RenewNIC{
			Device:     d.Device,
			Type:       d.Type,
			State:      d.State,
			Connection: d.Connection,
		})
	}
	return out, nil
}

// RenewIPv6 对指定网卡执行 reapply,触发其重新获取 IPv6 配置。
func (s *Service) RenewIPv6(ctx context.Context, iface string) IPv6RenewResult {
	if !lzcsdk.Available() {
		return IPv6RenewResult{Error: "lzc-sdk 不可用(非懒猫环境)"}
	}
	out, err := lzcsdk.ReapplyNIC(ctx, iface)
	if err != nil {
		logger.Warn("ipv6 renew failed iface=%s err=%v", iface, err)
		return IPv6RenewResult{Device: iface, Error: err.Error()}
	}
	logger.Info("ipv6 renew ok iface=%s out=%q", iface, strings.TrimSpace(out))
	return IPv6RenewResult{OK: true, Device: iface, Output: strings.TrimSpace(out)}
}

// ListContainers returns containers grouped by app (bridge).
