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
	dialOnce sync.Once
	conn     *grpc.ClientConn
	dialErr  error
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
func dial() (*grpc.ClientConn, error) {
	dialOnce.Do(func() {
		if !Available() {
			dialErr = errors.New("lzc-sdk: socket or app certs not present")
			return
		}
		cred, err := gohelper.BuildClientCredOption(gohelper.CAPath, gohelper.APPKeyPath, gohelper.APPCertPath)
		if err != nil {
			dialErr = fmt.Errorf("lzc-sdk build cred: %w", err)
			return
		}
		c, err := grpc.NewClient("unix://"+apiSocketPath, cred)
		if err != nil {
			dialErr = fmt.Errorf("lzc-sdk dial: %w", err)
			return
		}
		conn = c
	})
	return conn, dialErr
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

	if len(errs) > 0 && out.WiredStatus == "" && out.WirelessStatus == "" && out.Connectivity == "" {
		return out, fmt.Errorf("lzc-sdk: %s", strings.Join(errs, "; "))
	}
	return out, nil
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
		}
	}
	return out, nil
}
