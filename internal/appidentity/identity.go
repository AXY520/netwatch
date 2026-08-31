package appidentity

import (
	"net/url"
	"strings"
)

// Build returns the stable policy/storage identity for one Lazycat
// application instance. Single-instance applications deliberately retain the
// historical app_id key so existing state and API clients remain compatible.
func Build(appID, userID, project string, multiInstance bool) string {
	appID = strings.TrimSpace(appID)
	if appID == "" {
		return ""
	}
	if !multiInstance {
		return appID
	}
	if userID = strings.TrimSpace(userID); userID != "" {
		return appID + "@user:" + url.QueryEscape(userID)
	}
	if project = strings.TrimSpace(project); project != "" {
		return appID + "@project:" + url.QueryEscape(project)
	}
	return appID
}

// Base returns the application id portion of an instance identity.
func Base(instanceID string) string {
	instanceID = strings.TrimSpace(instanceID)
	for _, marker := range []string{"@user:", "@project:"} {
		if index := strings.LastIndex(instanceID, marker); index > 0 {
			return instanceID[:index]
		}
	}
	return instanceID
}
