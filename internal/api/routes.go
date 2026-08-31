package api

import "net/http"

func (h *Handler) Register(mux *http.ServeMux) {
	h.registerObservationRoutes(mux)
	h.registerSpeedRoutes(mux)
	h.registerNotificationRoutes(mux)
	h.registerTrafficRoutes(mux)
	h.registerNetworkControlRoutes(mux)
	h.registerContainerRoutes(mux)
	mux.HandleFunc("/metrics", h.handleMetrics)
}

func (h *Handler) registerObservationRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/healthz", h.handleHealth)
	mux.HandleFunc("/api/v1/capabilities", h.handleCapabilities)
	mux.HandleFunc("/api/v1/summary", h.handleSummary)
	mux.HandleFunc("/api/v1/connectivity/websites", h.handleWebsiteConnectivity)
	mux.HandleFunc("/api/v1/connectivity/websites/run", h.handleWebsiteRefresh)
	mux.HandleFunc("/api/v1/network", h.handleNetworkInfo)
	mux.HandleFunc("/api/v1/network/interfaces/refresh", h.handleNetworkInterfacesRefresh)
	mux.HandleFunc("/api/v1/network/nat/run", h.handleNATRefresh)
	mux.HandleFunc("/api/v1/probe/run", h.handleRefresh)
	mux.HandleFunc("/api/v1/timeseries", h.handleTimeseries)
	mux.HandleFunc("/api/v1/settings", h.handleSettings)
	mux.HandleFunc("/api/v1/diagnostics/trace", h.handleTrace)
	mux.HandleFunc("/api/v1/diagnostics/dns", h.handleDNSDiagnostic)
	mux.HandleFunc("/api/v1/diagnostics/trace/task", h.handleTraceTask)
	mux.HandleFunc("/api/v1/diagnostics/trace/cancel", h.handleTraceCancel)
	mux.HandleFunc("/api/v1/events", h.handleSSE)
	mux.HandleFunc("/api/v1/network/realtime", h.handleRealtimeNetStats)
	mux.HandleFunc("/api/v1/network/ports", h.handleHostPorts)
	mux.HandleFunc("/api/v1/network/egress-lookups", h.handleEgressLookups)
}

func (h *Handler) registerSpeedRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/v1/speed/config", h.handleSpeedConfig)
	mux.HandleFunc("/api/v1/speed/broadband/catalog", h.handleBroadbandCatalog)
	mux.HandleFunc("/api/v1/speed/broadband/server/start", h.handleBroadbandStart)
	mux.HandleFunc("/api/v1/speed/broadband/server/task", h.handleBroadbandTask)
	mux.HandleFunc("/api/v1/speed/broadband/server/cancel", h.handleBroadbandCancel)
	mux.HandleFunc("/api/v1/speed/broadband/client/result", h.handleBroadbandClientResult)
	mux.HandleFunc("/api/v1/speed/broadband/port-policy/start", h.handleBroadbandPortPolicyStart)
	mux.HandleFunc("/api/v1/speed/broadband/port-policy/task", h.handleBroadbandPortPolicyTask)
	mux.HandleFunc("/api/v1/speed/broadband/port-policy/cancel", h.handleBroadbandPortPolicyCancel)
	mux.HandleFunc("/api/v1/speed/broadband/history", h.handleBroadbandHistory)
	mux.HandleFunc("/api/v1/speed/local/history", h.handleLocalHistory)
	mux.HandleFunc("/api/v1/speed/history/note", h.handleSpeedHistoryNote)
	mux.HandleFunc("/api/v1/speed/history/clear", h.handleSpeedHistoryClear)
	mux.HandleFunc("/api/v1/speed/local/result", h.handleLocalResult)
	mux.HandleFunc("/api/v1/speed/local/ping", h.handleLocalPing)
	mux.HandleFunc("/api/v1/speed/local/download", h.handleLocalDownload)
	mux.HandleFunc("/api/v1/speed/local/upload", h.handleLocalUpload)
}

func (h *Handler) registerNotificationRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/v1/events/history", h.handleNetworkEventHistory)
	mux.HandleFunc("/api/v1/notifications/events", h.handleNotificationEvents)
	mux.HandleFunc("/api/v1/notifications/bark/test", h.handleBarkNotificationTest)
	mux.HandleFunc("/api/v1/notifications/pushplus/test", h.handlePushPlusNotificationTest)
	mux.HandleFunc("/api/v1/lan/devices", h.handleLANDevices)
	mux.HandleFunc("/api/v1/lan/devices/meta", h.handleLANDeviceMeta)
}

func (h *Handler) registerTrafficRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/v1/network/app-traffic", h.handleAppTraffic)
	mux.HandleFunc("/api/v1/network/app-traffic/history", h.handleAppTrafficHistory)
	mux.HandleFunc("/api/v1/network/app-traffic/limit", h.handleAppTrafficLimit)
	mux.HandleFunc("/api/v1/network/app-policy", h.handleAppNetworkPolicy)
	mux.HandleFunc("/api/v1/network/app-proxy/settings", h.handleAppProxySettings)
}

func (h *Handler) registerNetworkControlRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/v1/network/mutations/audit", h.handleNetworkMutationAudit)
	mux.HandleFunc("/api/v1/network/ipv6/renew-nics", h.handleIPv6RenewNICs)
	mux.HandleFunc("/api/v1/network/ipv6/renew", h.handleIPv6Renew)
	mux.HandleFunc("/api/v1/network/config/devices", h.handleNetworkConfigDevices)
	mux.HandleFunc("/api/v1/network/config/pending", h.handleNetworkConfigPending)
	mux.HandleFunc("/api/v1/network/config/check-ip", h.handleNetworkConfigCheckIP)
	mux.HandleFunc("/api/v1/network/config/apply", h.handleNetworkConfigApply)
	mux.HandleFunc("/api/v1/network/config/restart", h.handleNetworkConfigRestart)
	mux.HandleFunc("/api/v1/network/config/confirm", h.handleNetworkConfigConfirm)
	mux.HandleFunc("/api/v1/network/config/rollback", h.handleNetworkConfigRollback)
	mux.HandleFunc("/api/v1/network/dns", h.handleHostDNS)
	mux.HandleFunc("/api/v1/network/dns/apply", h.handleHostDNSApply)
	mux.HandleFunc("/api/v1/network/dns/confirm", h.handleHostDNSConfirm)
	mux.HandleFunc("/api/v1/network/dns/rollback", h.handleHostDNSRollback)
	mux.HandleFunc("/api/v1/network/dns/pending", h.handleHostDNSPending)
	mux.HandleFunc("/api/v1/network/bridges", h.handleHostBridges)
	mux.HandleFunc("/api/v1/network/bridges/create", h.handleHostBridgeCreate)
	mux.HandleFunc("/api/v1/network/bridges/confirm", h.handleHostBridgeConfirm)
	mux.HandleFunc("/api/v1/network/bridges/rollback", h.handleHostBridgeRollback)
	mux.HandleFunc("/api/v1/network/bridges/dissolve", h.handleHostBridgeDissolve)
	mux.HandleFunc("/api/v1/network/bridges/pending", h.handleHostBridgePending)
}

func (h *Handler) registerContainerRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/v1/containers", h.handleContainers)
	mux.HandleFunc("/api/v1/containers/block", h.handleContainerBlock)
	mux.HandleFunc("/api/v1/containers/unblock", h.handleContainerUnblock)
}
