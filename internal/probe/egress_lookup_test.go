package probe

import "testing"

func TestParseCipCCResponse(t *testing.T) {
	raw := "IP\t: 183.95.49.153\n地址\t: 中国 湖北 武汉\n运营商\t: 联通\n"
	lu, err := parseCipCCResponse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if lu.IP != "183.95.49.153" {
		t.Fatalf("ip=%q", lu.IP)
	}
	if lu.Country != "中国" || lu.Region != "湖北" || lu.City != "武汉" {
		t.Fatalf("geo=%q/%q/%q", lu.Country, lu.Region, lu.City)
	}
	if lu.ISP != "联通" {
		t.Fatalf("isp=%q", lu.ISP)
	}
}

func TestPickInternationalEgressMajority(t *testing.T) {
	// Three mainland, one flaky overseas → pick mainland majority IP.
	cands := []EgressLookup{
		{Provider: "a", IP: "1.1.1.1", Country: "China"},
		{Provider: "b", IP: "1.1.1.1", Country: "China"},
		{Provider: "c", IP: "1.1.1.1", Country: "CN"},
		{Provider: "d", IP: "9.9.9.9", Country: "Netherlands"},
	}
	got := pickInternationalEgress(cands)
	if got.IP != "1.1.1.1" {
		t.Fatalf("want majority 1.1.1.1, got %+v", got)
	}
}

func TestPickInternationalEgressOverseasAgreement(t *testing.T) {
	cands := []EgressLookup{
		{Provider: "a", IP: "8.8.8.8", Country: "United States"},
		{Provider: "b", IP: "8.8.8.8", Country: "US"},
		{Provider: "c", IP: "1.2.3.4", Country: "China"},
	}
	got := pickInternationalEgress(cands)
	if got.IP != "8.8.8.8" {
		t.Fatalf("want overseas majority, got %+v", got)
	}
}
