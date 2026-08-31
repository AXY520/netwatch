package probe

import (
	"strings"
)

// publicBroadbandNode describes one public CDN endpoint family used by both
// broadband modes. The browser receives the equivalent catalog from the web
// bundle, while the server uses this copy directly.
type publicBroadbandNode struct {
	ID            string   `json:"id"`
	Label         string   `json:"label"`
	Category      string   `json:"category"`
	Secure        bool     `json:"secure"`
	PingURLs      []string `json:"ping_urls"`
	DownloadURLs  []string `json:"download_urls"`
	UploadURLs    []string `json:"upload_urls"`
	BrowserUsable bool     `json:"browser_usable"`
	BrowserReason string   `json:"browser_reason,omitempty"`
}

var publicBroadbandNodes = []publicBroadbandNode{
	{
		ID: "1", Label: "综合 CDN 1", Category: "cdn", Secure: true, BrowserUsable: true,
		PingURLs: []string{"https://lf3-cdn-tos.bytecdntp.com/"},
		DownloadURLs: []string{
			"https://cdn.aixifan.com/downloads/AcfunLive-Setup-1.9.0.200-ReleaseX64_6d5c40.exe",
			"https://devtools.qiniu.com/linux/amd64/qrsctl",
			"https://devtools.qiniu.com/qdoractl-darwin-amd64-0.4.6",
			"https://gw.alipayobjects.com/os/volans-demo/93211a67-0eed-40ff-8a48-f6c137a88781/MiniProgramStudio-3.1.3.exe",
			"https://download.jr.jd.com/downapp/jrapp_jr9631.apk",
		},
		UploadURLs: []string{"https://mbd.baidu.com/ztbox?action=zpblog", "https://vcs.zijieapi.com/vc/setting?aid=6383&pageId=6241"},
	},
	{
		ID: "2", Label: "综合 CDN 2", Category: "cdn", Secure: true, BrowserUsable: true,
		PingURLs: []string{"https://lf3-cdn-tos.bytecdntp.com/"},
		DownloadURLs: []string{
			"https://downapp.sina.cn/m/06/sinaNews_8.27.0_1719288606_4386_3538_armeabi-v7a.apk",
			"https://statics.itc.cn/lt-app/sohumobile_official_gray_optimizeRelease_4_1.0.3_01161850.apk",
			"https://lf3-cdn-tos.bytegoofy.com/obj/douyin-pc-client/7044145585217083655/releases/8293088/1.0.8/win32-ia32/douyin-v1.0.8-win32-ia32-douyin.exe",
			"https://wwwstatic.vivo.com.cn/vivoportal/files/download/app/20231026/350bda07c8a0719919bcadbf5aea3538.apk",
			"https://cd.pddpic.com/android_dev/2024-06-26/06027b4121edcd1f106d992128a7124b.apk",
		},
		UploadURLs: []string{"https://mbd.baidu.com/ztbox?action=zpblog", "https://vcs.zijieapi.com/vc/setting?aid=6383&pageId=6241"},
	},
}

func publicBroadbandCatalog() []publicBroadbandNode {
	out := make([]publicBroadbandNode, len(publicBroadbandNodes))
	copy(out, publicBroadbandNodes)
	return out
}

func (s *Service) GetPublicBroadbandNodes() []publicBroadbandNode {
	return publicBroadbandCatalog()
}

func publicBroadbandNodeByID(id string) (publicBroadbandNode, bool) {
	for _, node := range publicBroadbandNodes {
		if node.ID == strings.TrimSpace(id) {
			return node, true
		}
	}
	return publicBroadbandNode{}, false
}
