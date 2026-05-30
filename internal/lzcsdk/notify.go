package lzcsdk

import (
	"context"
	"encoding/binary"
	"fmt"
	"net/url"
	"sync"
	"time"

	gohelper "gitee.com/linakesi/lzc-sdk/lang/go"
	"gitee.com/linakesi/lzc-sdk/lang/go/common"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

var (
	gwOnce sync.Once
	gwVal  *gohelper.APIGateway
	gwErr  error
)

func gw() (*gohelper.APIGateway, error) {
	gwOnce.Do(func() {
		if !Available() {
			gwErr = fmt.Errorf("lzc runtime not available")
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		gwVal, gwErr = gohelper.NewAPIGateway(ctx)
	})
	return gwVal, gwErr
}

// GatewayAvailable reports whether the Lazycat runtime is present.
func GatewayAvailable() bool {
	return Available()
}

// Device mirrors a Lazycat end device for notification targeting.
type Device struct {
	ID           string
	Name         string
	Model        string
	RemarkName   string
	IsOnline     bool
	DeviceAPIURL string
}

// ListDevices enumerates all end devices via the Lazycat gateway.
func ListDevices(ctx context.Context) ([]Device, error) {
	g, err := gw()
	if err != nil {
		return nil, err
	}
	resp, err := g.Devices.ListEndDevices(ctx, &common.ListEndDeviceRequest{})
	if err != nil {
		return nil, fmt.Errorf("list devices: %w", err)
	}
	out := make([]Device, 0, len(resp.Devices))
	for _, d := range resp.Devices {
		out = append(out, Device{
			ID:           d.GetUniqueDeivceId(),
			Name:         d.GetName(),
			Model:        d.GetModel(),
			RemarkName:   d.GetRemarkName(),
			IsOnline:     d.GetIsOnline(),
			DeviceAPIURL: d.GetDeviceApiUrl(),
		})
	}
	return out, nil
}

// tokenCred implements grpc.PerRPCCredentials with a static auth token.
type tokenCred struct{ token string }

func (t tokenCred) GetRequestMetadata(context.Context, ...string) (map[string]string, error) {
	return map[string]string{"lzc_dapi_auth_token": t.token}, nil
}

func (t tokenCred) RequireTransportSecurity() bool { return false }

// NotifyDevice sends a system notification to a device via its DeviceAPIURL.
func NotifyDevice(ctx context.Context, deviceAPIURL, title, body, deeplinkURL string) error {
	if deviceAPIURL == "" {
		return fmt.Errorf("empty device API URL")
	}

	parsed, err := url.Parse(deviceAPIURL)
	if err != nil {
		return fmt.Errorf("parse device URL: %w", err)
	}
	host := parsed.Host

	cred, err := gohelper.BuildClientCredOption(gohelper.CAPath, gohelper.APPKeyPath, gohelper.APPCertPath)
	if err != nil {
		return fmt.Errorf("build cred: %w", err)
	}

	// Connect to device with mTLS.
	dialCtx, dialCancel := context.WithTimeout(ctx, 5*time.Second)
	defer dialCancel()
	conn, err := grpc.DialContext(dialCtx, host, cred, grpc.WithBlock())
	if err != nil {
		// Fallback insecure for dev environments.
		conn, err = grpc.DialContext(dialCtx, host, grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithBlock())
		if err != nil {
			return fmt.Errorf("dial device %s: %w", host, err)
		}
	}
	defer conn.Close()

	// Get auth token from device's PermissionManager.
	at, err := gohelper.RequestAuthToken(ctx, conn)
	if err != nil {
		return fmt.Errorf("auth token: %w", err)
	}

	// Invoke NotificationService/Notify with auth token.
	method := "/cloud.lazycat.apis.localdevice.NotificationService/Notify"
	reqBytes := encodeNotifyRequest(title, body, deeplinkURL)
	var respBytes []byte
	err = conn.Invoke(ctx, method, reqBytes, &respBytes, grpc.PerRPCCredentials(tokenCred{at.Token}))
	if err != nil {
		return fmt.Errorf("notify invoke: %w", err)
	}
	return nil
}

// encodeNotifyRequest manually encodes a NotifyRequest protobuf message.
// Fields: 1=title(string), 2=body(string), 3=deeplink_url(string, optional).
func encodeNotifyRequest(title, body, deeplinkURL string) []byte {
	var buf []byte
	buf = appendProtoString(buf, 1, title)
	buf = appendProtoString(buf, 2, body)
	if deeplinkURL != "" {
		buf = appendProtoString(buf, 3, deeplinkURL)
	}
	return buf
}

func appendProtoString(buf []byte, fieldNum int, val string) []byte {
	buf = appendVarint(buf, uint64((fieldNum<<3)|2))
	buf = appendVarint(buf, uint64(len(val)))
	return append(buf, val...)
}

func appendVarint(buf []byte, v uint64) []byte {
	var tmp [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(tmp[:], v)
	return append(buf, tmp[:n]...)
}
