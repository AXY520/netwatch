package probe

import (
	"time"

	"github.com/mdlayher/wifi"
)

const wifiLinkSpeedTimeout = 750 * time.Millisecond

// readWiFiLinkSpeedMbps reads the current per-direction PHY rates from
// nl80211, the same kernel interface used by `iw dev <iface> link`.
//
// The Lazycat NetworkManager status exposes one machine-wide link_speed value
// without identifying its interface. On a host with both Ethernet and Wi-Fi
// connected that value can be the Ethernet rate, so it must not be applied to
// a Wi-Fi interface.
func readWiFiLinkSpeedMbps(ifaceName string) (rxMbps, txMbps float64) {
	client, err := wifi.New()
	if err != nil {
		return 0, 0
	}
	defer client.Close()

	_ = client.SetDeadline(time.Now().Add(wifiLinkSpeedTimeout))
	interfaces, err := client.Interfaces()
	if err != nil {
		return 0, 0
	}

	for _, iface := range interfaces {
		if iface.Name != ifaceName {
			continue
		}
		stations, err := client.StationInfo(iface)
		if err != nil || len(stations) == 0 {
			return 0, 0
		}
		return stationLinkSpeedMbps(stations[0])
	}

	return 0, 0
}

func stationLinkSpeedMbps(info *wifi.StationInfo) (rxMbps, txMbps float64) {
	if info == nil {
		return 0, 0
	}
	return linkSpeedMbpsFromBps(int64(info.ReceiveBitrate)),
		linkSpeedMbpsFromBps(int64(info.TransmitBitrate))
}
