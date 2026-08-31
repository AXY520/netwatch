package probe

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

var lanResolver = &net.Resolver{PreferGo: true, StrictErrors: true}

func lookupLANHostname(ip string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Millisecond)
	defer cancel()
	return lookupLANHostnameCtx(ctx, ip)
}

func lookupLANHostnameCtx(ctx context.Context, ip string) string {
	if strings.TrimSpace(ip) == "" {
		return ""
	}
	names, err := lanResolver.LookupAddr(ctx, ip)
	if err != nil || len(names) == 0 {
		return ""
	}
	name := strings.TrimSuffix(names[0], ".")
	if name == "" || strings.HasSuffix(name, ".in-addr.arpa") || strings.HasSuffix(name, ".ip6.arpa") {
		return ""
	}
	return name
}

// fillLANHostnamesBounded reverse-resolves only confirmed/reachable neighbors
// that still lack a hostname. Concurrency and per-lookup timeout are capped so
// a broken DNS path cannot block LAN scan completion.
func fillLANHostnamesBounded(ctx context.Context, devices []LANDevice, maxWorkers int, perLookup time.Duration) {
	if len(devices) == 0 {
		return
	}
	if maxWorkers < 1 {
		maxWorkers = 1
	}
	if perLookup <= 0 {
		perLookup = 120 * time.Millisecond
	}

	type job struct {
		idx int
		ip  string
	}
	var queue []job
	for i := range devices {
		if devices[i].Hostname != "" || devices[i].IP == "" {
			continue
		}
		// Skip incomplete/failed ARP noise created by warm-up floods.
		if !lanNeighborConfirmsOnline(devices[i].Reachability) && devices[i].Reachability != "arp-cache" {
			continue
		}
		queue = append(queue, job{idx: i, ip: devices[i].IP})
	}
	if len(queue) == 0 {
		return
	}

	workers := maxWorkers
	if workers > len(queue) {
		workers = len(queue)
	}
	jobs := make(chan job, len(queue))
	for _, j := range queue {
		jobs <- j
	}
	close(jobs)

	var wg sync.WaitGroup
	var mu sync.Mutex
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				select {
				case <-ctx.Done():
					return
				default:
				}
				lctx, cancel := context.WithTimeout(ctx, perLookup)
				name := lookupLANHostnameCtx(lctx, j.ip)
				cancel()
				if name == "" {
					continue
				}
				mu.Lock()
				if devices[j.idx].Hostname == "" {
					devices[j.idx].Hostname = name
				}
				mu.Unlock()
			}
		}()
	}

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
	case <-time.After(2 * time.Second):
		// Absolute ceiling for hostname enrichment during scan.
	}
}

func macAddressHint(mac string) string {
	mac = normalizeMAC(mac)
	parts := strings.Split(mac, ":")
	if len(parts) != 6 {
		return ""
	}
	first, err := strconv.ParseUint(parts[0], 16, 8)
	if err != nil {
		return ""
	}
	if first&0x02 != 0 {
		return "本地随机 MAC"
	}
	if vendor := lookupOUIVendor(parts[0] + parts[1] + parts[2]); vendor != "" {
		return vendor
	}
	return "未知厂商"
}

func lookupOUIVendor(prefix string) string {
	prefix = normalizeOUIPrefix(prefix)
	if prefix == "" {
		return ""
	}
	if vendor := lookupOUIVendorFromFiles(prefix); vendor != "" {
		return vendor
	}
	return fallbackOUIVendors()[prefix]
}

var ouiVendorCache struct {
	once    sync.Once
	vendors map[string]string
}

func lookupOUIVendorFromFiles(prefix string) string {
	ouiVendorCache.once.Do(func() {
		ouiVendorCache.vendors = loadOUIVendors()
	})
	return ouiVendorCache.vendors[prefix]
}

func loadOUIVendors() map[string]string {
	out := map[string]string{}
	paths := []string{
		"/usr/share/nmap/nmap-mac-prefixes",
		"/usr/share/misc/oui.txt",
		"/usr/share/ieee-data/oui.txt",
		"/var/lib/ieee-data/oui.txt",
		"/etc/oui.txt",
	}
	for _, path := range paths {
		f, err := os.Open(path)
		if err != nil {
			continue
		}
		parseOUIVendorFile(f, out)
		f.Close()
	}
	return out
}

func parseOUIVendorFile(r io.Reader, out map[string]string) {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		prefix := normalizeOUIPrefix(fields[0])
		if prefix == "" {
			continue
		}
		vendor := ""
		if strings.EqualFold(fields[1], "(hex)") || strings.EqualFold(fields[1], "(base") {
			if i := strings.Index(line, ")"); i >= 0 && i+1 < len(line) {
				vendor = strings.TrimSpace(line[i+1:])
			}
		} else {
			vendor = strings.TrimSpace(line[len(fields[0]):])
		}
		vendor = strings.Join(strings.Fields(vendor), " ")
		if vendor != "" {
			out[prefix] = vendor
		}
	}
}

func normalizeOUIPrefix(prefix string) string {
	prefix = strings.ToUpper(strings.TrimSpace(prefix))
	var b strings.Builder
	for _, ch := range prefix {
		if (ch >= '0' && ch <= '9') || (ch >= 'A' && ch <= 'F') {
			b.WriteRune(ch)
			if b.Len() == 6 {
				break
			}
		}
	}
	if b.Len() != 6 {
		return ""
	}
	return b.String()
}

var fallbackOUIVendorData = map[string]string{
	"001122": "Cisco",
	"001A11": "Google",
	"001B63": "Apple",
	"001D4F": "Apple",
	"002248": "Microsoft",
	"002332": "Apple",
	"00236C": "Apple",
	"002500": "Apple",
	"00259C": "Cisco-Linksys",
	"0026BB": "Apple",
	"0050E4": "Apple",
	"0050F2": "Microsoft",
	"006171": "Apple",
	"007D60": "Ubiquiti",
	"008865": "Apple",
	"00A040": "Apple",
	"00E04C": "Realtek",
	"042B58": "Shenzhen Hanzsung Technology",
	"10AE60": "Private",
	"14CC20": "TP-Link",
	"18FE34": "Espressif",
	"1C1B0D": "Giga-Byte",
	"1C36BB": "Apple",
	"20A2E4": "Apple",
	"24A160": "Espressif",
	"28CFE9": "Apple",
	"2C54CF": "LG",
	"34AB37": "Apple",
	"3C22FB": "Apple",
	"3C5A37": "Samsung",
	"3C7C3F": "Apple",
	"44D884": "Apple",
	"50C7BF": "TP-Link",
	"5C497D": "Samsung",
	"60F81D": "Apple",
	"64B0A6": "Apple",
	"6C4008": "Apple",
	"701CE7": "Intel",
	"748114": "Apple",
	"7824AF": "ASUSTek",
	"784F43": "Apple",
	"7C04D0": "Apple",
	"7C2EDD": "Samsung",
	"80E650": "Apple",
	"843A4B": "Intel",
	"847303": "Letv",
	"881FA1": "Apple",
	"8C8590": "Apple",
	"98E743": "Dell",
	"A020A6": "Espressif",
	"A4C138": "Telink",
	"A4D1D2": "Apple",
	"A85B36": "Huawei",
	"ACBC32": "Apple",
	"B827EB": "Raspberry Pi",
	"BC5436": "Apple",
	"C0A53E": "Apple",
	"C8BCC8": "Apple",
	"D0C5D3": "Apple",
	"D8BB2C": "Apple",
	"DC2B2A": "Apple",
	"E0B9A5": "Apple",
	"E4A7A0": "Intel",
	"E4CE8F": "Apple",
	"F0D5BF": "Intel",
	"F4F5D8": "Google",
	"F8FF0B": "Apple",
}

func fallbackOUIVendors() map[string]string {
	return fallbackOUIVendorData
}

func lanDeviceBody(dev LANDevice) string {
	name := firstNonEmpty(dev.Note, dev.Hostname, "未知设备")
	parts := []string{fmt.Sprintf("设备名称：%s", name)}
	parts = append(parts, fmt.Sprintf("IP 地址：%s", dev.IP))
	if len(dev.IPv6) > 0 {
		parts = append(parts, fmt.Sprintf("IPv6：%s", strings.Join(dev.IPv6, ", ")))
	}
	parts = append(parts, fmt.Sprintf("MAC 地址：%s", dev.MAC))
	if dev.VendorHint != "" {
		parts = append(parts, fmt.Sprintf("厂商：%s", dev.VendorHint))
	}
	if dev.Interface != "" {
		parts = append(parts, fmt.Sprintf("接口：%s", dev.Interface))
	}
	if len(dev.DetectionMethods) > 0 {
		parts = append(parts, fmt.Sprintf("检测方式：%s", strings.Join(dev.DetectionMethods, ", ")))
	}
	if dev.LastSeen != "" {
		parts = append(parts, fmt.Sprintf("最后在线：%s", dev.LastSeen))
	}
	parts = append(parts, fmt.Sprintf("检测时间：%s", localTimestamp()))
	return strings.Join(parts, "\n")
}

func isPrivateLANIPv4(ip net.IP) bool {
	v4 := ip.To4()
	if v4 == nil {
		return false
	}
	return v4[0] == 10 ||
		(v4[0] == 172 && v4[1] >= 16 && v4[1] <= 31) ||
		(v4[0] == 192 && v4[1] == 168) ||
		(v4[0] == 169 && v4[1] == 254)
}
