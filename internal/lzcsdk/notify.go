package lzcsdk

import (
	"context"
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

	// Get current user's UID. Try ListUIDs and use the first one.
	uidsResp, err := g.Users.ListUIDs(ctx, &common.ListUIDsRequest{})
	if err != nil {
		return nil, fmt.Errorf("list uids: %w", err)
	}
	if len(uidsResp.Uids) == 0 {
		return nil, fmt.Errorf("no users found")
	}
	uid := uidsResp.Uids[0]

	resp, err := g.Devices.ListEndDevices(ctx, &common.ListEndDeviceRequest{Uid: uid})
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

	// Build NotifyRequest message.
	req := &notifyRequest{
		Title:       title,
		Body:        body,
		DeeplinkURL: deeplinkURL,
	}

	// Invoke NotificationService/Notify with auth token.
	method := "/cloud.lazycat.apis.localdevice.NotificationService/Notify"
	var resp notifyResponse
	err = conn.Invoke(ctx, method, req, &resp, grpc.PerRPCCredentials(tokenCred{at.Token}))
	if err != nil {
		return fmt.Errorf("notify invoke: %w", err)
	}
	return nil
}

// notifyRequest implements proto.Message for NotificationService.Notify request.
type notifyRequest struct {
	Title       string
	Body        string
	DeeplinkURL string
}

func (m *notifyRequest) Reset()         { *m = notifyRequest{} }
func (m *notifyRequest) String() string { return fmt.Sprintf("title:%s body:%s", m.Title, m.Body) }
func (*notifyRequest) ProtoMessage()    {}

func (m *notifyRequest) Marshal() ([]byte, error) {
	var buf []byte
	if m.Title != "" {
		buf = appendProtoString(buf, 1, m.Title)
	}
	if m.Body != "" {
		buf = appendProtoString(buf, 2, m.Body)
	}
	if m.DeeplinkURL != "" {
		buf = appendProtoString(buf, 3, m.DeeplinkURL)
	}
	return buf, nil
}

func (m *notifyRequest) Unmarshal([]byte) error {
	return fmt.Errorf("unmarshal not implemented")
}

// notifyResponse implements proto.Message for NotificationService.Notify response (empty).
type notifyResponse struct{}

func (m *notifyResponse) Reset()         {}
func (m *notifyResponse) String() string { return "" }
func (*notifyResponse) ProtoMessage()    {}

func (m *notifyResponse) Marshal() ([]byte, error) {
	return []byte{}, nil
}

func (m *notifyResponse) Unmarshal([]byte) error {
	return nil
}

func appendProtoString(buf []byte, fieldNum int, val string) []byte {
	// field tag: (fieldNum << 3) | wireType(2=length-delimited)
	buf = appendVarint(buf, uint64((fieldNum<<3)|2))
	buf = appendVarint(buf, uint64(len(val)))
	return append(buf, val...)
}

func appendVarint(buf []byte, v uint64) []byte {
	for v >= 0x80 {
		buf = append(buf, byte(v)|0x80)
		v >>= 7
	}
	return append(buf, byte(v))
}
