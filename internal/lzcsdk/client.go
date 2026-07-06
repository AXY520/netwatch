package lzcsdk

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	gohelper "gitee.com/linakesi/lzc-sdk/lang/go"
	syspb "gitee.com/linakesi/lzc-sdk/lang/go/sys"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/emptypb"
)

const (
	apiSocketPath = "/lzcapp/run/sys/lzc-apis.socket"
	appCertPath   = "/lzcapp/run/certs/app.crt"
)

var (
	dialMu       sync.Mutex
	conn         *grpc.ClientConn
	dialErr      error
	dialLastFail time.Time
)

// Available reports whether the lzc-apis socket and app certificates are
// present in this environment. The runtime requires mTLS so both are needed.
func Available() bool {
	if _, err := os.Stat(apiSocketPath); err != nil {
		return false
	}
	if _, err := os.Stat(appCertPath); err != nil {
		return false
	}
	return true
}

// dial connects to the lzc-apiserver over the local unix socket with mTLS.
//
// We deliberately bypass gohelper.NewAPIConn: when LZCAPP_API_GATEWAY_ADDRESS
// is set (which it always is — e.g. `app.cloud.lazycat.app.netwatch.lzcapp:81`),
// the official helper switches to a TCP gateway on the lzc bridge network,
// but our netwatch service runs in `network_mode: host` and can't resolve
// the `*.lzcapp` lzcdns name — every dial then times out as
// "context deadline exceeded". The unix socket path works regardless of
// network mode and is the right transport for system-side queries.
//
// 失败后 30 秒内不重试，避免频繁日志和无效调用。
func dial() (*grpc.ClientConn, error) {
	dialMu.Lock()
	defer dialMu.Unlock()

	// 已有连接且无错误，直接返回
	if conn != nil && dialErr == nil {
		return conn, nil
	}
	// 失败后 30 秒冷却期内不重试
	if dialErr != nil && time.Since(dialLastFail) < 30*time.Second {
		return nil, dialErr
	}

	if !Available() {
		dialErr = errors.New("lzc-sdk: socket or app certs not present")
		dialLastFail = time.Now()
		return nil, dialErr
	}
	cred, err := gohelper.BuildClientCredOption(gohelper.CAPath, gohelper.APPKeyPath, gohelper.APPCertPath)
	if err != nil {
		dialErr = fmt.Errorf("lzc-sdk build cred: %w", err)
		dialLastFail = time.Now()
		return nil, dialErr
	}
	c, err := grpc.NewClient("unix://"+apiSocketPath, cred)
	if err != nil {
		dialErr = fmt.Errorf("lzc-sdk dial: %w", err)
		dialLastFail = time.Now()
		return nil, dialErr
	}
	conn = c
	dialErr = nil
	return conn, nil
}

// markStale 标记当前连接为失效，下次 dial() 会重新建立连接。
func markStale() {
	dialMu.Lock()
	conn = nil
	dialErr = errors.New("lzc-sdk: connection marked stale")
	dialLastFail = time.Time{} // 立即允许重试
	dialMu.Unlock()
}

// NetStatus is a flattened view of sys.NetworkManager.Status() plus a
// connectivity probe. All fields are optional — zero value means unknown.
type NetStatus struct {
	HasInternet    bool
	WiredStatus    string // connected/disconnected/disabled/connecting/disconnecting/unavailable/unknown
	WirelessStatus string
	LinkSpeedBps   int64 // raw value from sys.NetworkManager.Status (bits/sec)
	Wifi           WifiInfo
	Connectivity   string // Full/Limited/Portal/None/Unknown — empty when probe was skipped
}

type WifiInfo struct {
	SSID      string
	BSSID     string
	Signal    int32 // 0..100
	Frequency int32
	Security  bool
	Connected bool
}

// FetchNetworkStatus calls Status() and Connectivity() concurrently.
// 2s per RPC; failures are returned but partial results still populate the struct.
// 连接错误时自动标记 stale，下次调用会重连。
func FetchNetworkStatus(ctx context.Context) (NetStatus, error) {
	cc, err := dial()
	if err != nil {
		return NetStatus{}, err
	}
	cli := syspb.NewNetworkManagerClient(cc)

	var (
		out  NetStatus
		errs []string
		wg   sync.WaitGroup
		mu   sync.Mutex
	)

	wg.Add(2)
	go func() {
		defer wg.Done()
		c, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		st, err := cli.Status(c, &emptypb.Empty{})
		mu.Lock()
		defer mu.Unlock()
		if err != nil {
			errs = append(errs, "Status: "+err.Error())
			return
		}
		out.HasInternet = st.HasInternet
		out.WiredStatus = deviceStatusName(st.WiredDevice)
		out.WirelessStatus = deviceStatusName(st.WirelessDevice)
		out.LinkSpeedBps = st.LinkSpeed
		if ap := st.Info; ap != nil {
			out.Wifi = WifiInfo{
				SSID:      ap.Ssid,
				BSSID:     ap.Bssid,
				Signal:    ap.Signal,
				Frequency: ap.Frequency,
				Security:  ap.Security,
				Connected: ap.Connected,
			}
		}
	}()
	go func() {
		defer wg.Done()
		c, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		// 优先 GetConnectivity（lzc-apiserver 上 Connectivity 当前未实现，返回
		// "Unimplemented"）。两个 RPC 返回同一个枚举，只是消息壳子不一样。
		rep, err := cli.GetConnectivity(c, &emptypb.Empty{})
		mu.Lock()
		defer mu.Unlock()
		if err != nil {
			errs = append(errs, "GetConnectivity: "+err.Error())
			return
		}
		out.Connectivity = rep.Connectivity.String()
	}()
	wg.Wait()

	if len(errs) > 0 {
		// 两个 RPC 都失败说明连接可能断了，标记 stale 允许重连
		if len(errs) >= 2 || isConnectionError(errs) {
			markStale()
		}
		if out.WiredStatus == "" && out.WirelessStatus == "" && out.Connectivity == "" {
			return out, fmt.Errorf("lzc-sdk: %s", strings.Join(errs, "; "))
		}
	}
	return out, nil
}

func isConnectionError(errs []string) bool {
	for _, e := range errs {
		el := strings.ToLower(e)
		if strings.Contains(el, "connection") || strings.Contains(el, "broken pipe") ||
			strings.Contains(el, "eof") || strings.Contains(el, "unavailable") ||
			strings.Contains(el, "transport is closing") {
			return true
		}
	}
	return false
}

func deviceStatusName(s syspb.NetworkDeviceStatus) string {
	switch s {
	case syspb.NetworkDeviceStatus_NetworkDeviceStatusUnavailable:
		return "unavailable"
	case syspb.NetworkDeviceStatus_NetworkDeviceStatusDisconnected:
		return "disconnected"
	case syspb.NetworkDeviceStatus_NetworkDeviceStatusConnecting:
		return "connecting"
	case syspb.NetworkDeviceStatus_NetworkDeviceStatusConnected:
		return "connected"
	case syspb.NetworkDeviceStatus_NetworkDeviceStatusDisconnecting:
		return "disconnecting"
	case syspb.NetworkDeviceStatus_NetworkDeviceStatusDisabled:
		return "disabled"
	default:
		return "unknown"
	}
}

// AppInfo is a stripped view of sys.PackageManager.QueryApplication's result.
type AppInfo struct {
	AppID  string
	Title  string // 用户可见的应用名（如 "网络监测"）；缺省时回落到 AppID
	Domain string
	Icon   string // 图标 URL，如 https://$boxdomain/sys/icons/$appid.png
}

// ListApps queries the PackageManager for installed applications. Returns
// a map keyed by appid for easy joining with docker bridge mapping data.
func ListApps(ctx context.Context) (map[string]AppInfo, error) {
	cc, err := dial()
	if err != nil {
		return nil, err
	}
	cli := syspb.NewPackageManagerClient(cc)
	c, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	resp, err := cli.QueryApplication(c, &syspb.QueryApplicationRequest{})
	if err != nil {
		if isConnectionError([]string{err.Error()}) {
			markStale()
		}
		return nil, err
	}
	out := make(map[string]AppInfo, len(resp.InfoList))
	for _, a := range resp.InfoList {
		title := a.GetTitle()
		if title == "" {
			title = a.Appid
		}
		out[a.Appid] = AppInfo{
			AppID:  a.Appid,
			Title:  title,
			Domain: a.GetDomain(),
			Icon:   a.GetIcon(),
		}
	}
	return out, nil
}

// NMDevice 是一块由 NetworkManager 管理的网络设备(用于 IPv6 续约选择)。
type NMDevice struct {
	Device     string // 网卡名,如 enp2s0 / wlp4s0
	Type       string // ethernet / wifi / ...
	State      string // connected / disconnected / unavailable / unmanaged ...
	Connection string // 活动连接名,如 "Wired connection 1"
}

// nmcliCall 调用系统 NetworkManager 的 nmcli(经 lzc-sdk),返回其标准输出。
// args 原样作为 nmcli 的命令行参数,不经过 shell,无注入风险。
func nmcliCall(ctx context.Context, args []string, timeout time.Duration) (string, error) {
	cc, err := dial()
	if err != nil {
		return "", err
	}
	cli := syspb.NewNetworkManagerClient(cc)
	c, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	reply, err := cli.NmcliCall(c, &syspb.NmcliCallRequest{Args: args})
	if err != nil {
		if isConnectionError([]string{err.Error()}) {
			markStale()
		}
		return "", err
	}
	return reply.GetOut(), nil
}

// NmcliCall exposes a constrained nmcli call for higher-level network config
// workflows. Arguments are passed directly to the system API, never through a
// shell.
func NmcliCall(ctx context.Context, args []string, timeout time.Duration) (string, error) {
	return nmcliCall(ctx, args, timeout)
}

// ListReapplicableNICs 返回当前可对其执行 IPv6 续约的网卡:
// 即由 NetworkManager 管理、已连接(connected)且类型为 ethernet/wifi 的物理网卡。
// 过滤掉 bridge/veth/tun/loopback/unmanaged 等虚拟或不可操作设备。
func ListReapplicableNICs(ctx context.Context) ([]NMDevice, error) {
	// terse 模式输出: DEVICE:TYPE:STATE:CONNECTION,字段以 ':' 分隔(冒号转义为 '\:')
	out, err := nmcliCall(ctx, []string{"-t", "-f", "DEVICE,TYPE,STATE,CONNECTION", "device", "status"}, 5*time.Second)
	if err != nil {
		return nil, err
	}
	var devices []NMDevice
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := splitNmcliTerse(line)
		if len(fields) < 3 {
			continue
		}
		dev := NMDevice{Device: fields[0], Type: fields[1], State: fields[2]}
		if len(fields) >= 4 {
			dev.Connection = fields[3]
		}
		if dev.Type != "ethernet" && dev.Type != "wifi" {
			continue
		}
		// 只保留已连接、且属于真实物理网卡的(排除 veth/容器虚拟网卡)。
		if !strings.HasPrefix(dev.State, "connected") {
			continue
		}
		if strings.HasPrefix(dev.Device, "veth") || strings.HasPrefix(dev.Device, "lzc-br") {
			continue
		}
		devices = append(devices, dev)
	}
	return devices, nil
}

// ReapplyNIC 对指定网卡执行 `nmcli device reapply`,触发其重新获取 IP(含 IPv6)配置。
// 这是热重应用,比 `connection up` 温和,通常不会中断已有连接。
func ReapplyNIC(ctx context.Context, iface string) (string, error) {
	iface = strings.TrimSpace(iface)
	if iface == "" {
		return "", errors.New("lzc-sdk: empty interface name")
	}
	return nmcliCall(ctx, []string{"device", "reapply", iface}, 15*time.Second)
}

// splitNmcliTerse 按 nmcli terse 模式的规则拆分一行:字段以 ':' 分隔,
// 字段内的字面冒号会被转义为 '\:'。
func splitNmcliTerse(line string) []string {
	var fields []string
	var cur strings.Builder
	for i := 0; i < len(line); i++ {
		c := line[i]
		if c == '\\' && i+1 < len(line) && line[i+1] == ':' {
			cur.WriteByte(':')
			i++
			continue
		}
		if c == ':' {
			fields = append(fields, cur.String())
			cur.Reset()
			continue
		}
		cur.WriteByte(c)
	}
	fields = append(fields, cur.String())
	return fields
}
