package probe

import (
	"math"
	"testing"

	"github.com/mdlayher/wifi"
)

func TestStationLinkSpeedMbps(t *testing.T) {
	rx, tx := stationLinkSpeedMbps(&wifi.StationInfo{
		ReceiveBitrate:  720_600_000,
		TransmitBitrate: 864_800_000,
	})
	if math.Abs(rx-720.6) > 0.001 || math.Abs(tx-864.8) > 0.001 {
		t.Fatalf("station rates = %g/%g Mbps, want 720.6/864.8", rx, tx)
	}
}

func TestStationLinkSpeedMbpsNil(t *testing.T) {
	rx, tx := stationLinkSpeedMbps(nil)
	if rx != 0 || tx != 0 {
		t.Fatalf("nil station rates = %g/%g Mbps, want 0/0", rx, tx)
	}
}
