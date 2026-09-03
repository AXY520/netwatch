package probe

import "strings"

// Application traffic rules are intentionally kept in this file so the two
// independent lists can be maintained without searching through collectors or
// network-control implementations.
//
// To keep an application visible in traffic trends but forbid bandwidth
// limits, proxying, and internet blocking, add it only to whitelistedAppIDs.
// To hide an application from traffic trends entirely, add it to
// excludedTrafficAppIDs instead.

// whitelistedAppIDs contains applications that may be observed but must never
// receive a network-control policy.
var whitelistedAppIDs = map[string]bool{
	"cloud.lazycat.app.photo":       true,
	"cloud.lazycat.shell.files":     true,
	"cloud.lazycat.shell.appstore":  true,
	"cloud.lazycat.app.ai":          true,
	"cloud.lazycat.developer.tools": true,
	"cloud.lazycat.app.forward":     true,
	"cloud.lazycat.app.camera":      true,
	"cloud.lazycat.totoro":          true,
	"cloud.lazycat.lightos.entry":   true,
}

// Title matching is a compatibility fallback for older runtime records that
// do not carry a reliable application ID.
var whitelistedAppTitleKeywords = []string{
	"懒猫相册",
	"懒猫网盘",
	"懒猫商店",
	"ai pod",
	"懒猫开发者工具",
	"局域网端口转发",
	"懒猫摄像头",
}

// excludedTrafficAppIDs contains system applications that must not appear in
// traffic trends. An entry here is stronger than the control whitelist above.
var excludedTrafficAppIDs = map[string]bool{
	"cloud.lazycat.shell.settings": true,
	"cloud.lazycat.shell.backup":   true,
	"cloud.lazycat.app.forward":    true,
}

var excludedTrafficAppTitleKeywords = []string{
	"系统设置",
	"system settings",
	"备份和还原",
	"backup and restore",
}

func isWhitelistedApp(appID, title string) bool {
	if whitelistedAppIDs[strings.TrimSpace(appID)] {
		return true
	}
	return appTitleContainsAny(title, whitelistedAppTitleKeywords)
}

func isExcludedApp(appID, title string) bool {
	if excludedTrafficAppIDs[strings.TrimSpace(appID)] {
		return true
	}
	return appTitleContainsAny(title, excludedTrafficAppTitleKeywords)
}

func appTitleContainsAny(title string, keywords []string) bool {
	title = strings.ToLower(strings.TrimSpace(title))
	for _, keyword := range keywords {
		if strings.Contains(title, strings.ToLower(keyword)) {
			return true
		}
	}
	return false
}
