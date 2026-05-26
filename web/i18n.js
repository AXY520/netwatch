(function () {
    if (window.__) return;

    window.escapeHtml = function (value) {
        return String(value ?? '')
            .replaceAll('&', '&amp;')
            .replaceAll('<', '&lt;')
            .replaceAll('>', '&gt;')
            .replaceAll('"', '&quot;')
            .replaceAll("'", '&#39;');
    };

    var dict = {
        "zh-CN": {
            "ok": "正常",
            "down": "故障",
            "degraded": "降级",
            "unknown": "未知",
            "starting": "准备中",
            "latency": "延迟采样",
            "download": "下载测速",
            "upload": "上传测速",
            "finalizing": "整理结果",
            "complete": "已完成",
            "canceled": "已停止",
            "error": "错误",
            "failed": "失败",
            "idle": "待启动",
            "standby": "等待测速开始",
            "waiting": "等待中",
            "loading": "加载中",
            "searching": "查询中",
            "no_data": "无数据",
            "no_results": "暂无检测结果",
            "no_records": "暂无记录",
            "preparing": "准备中",
            "dl_speedtest": "下载测速",
            "ul_speedtest": "上传测速",
            "speedtest_complete": "测速完成",
            "speedtest_stopped": "测速已停止",
            "speedtest_failed": "测速失败",
            "manual_stop": "测速已手动停止",
            "transfer_done": "网页到本机传输测速完成",
            "transfer_stopped": "网页到本机传输测速已停止",
            "transfer_start_failed": "网页到本机传输测速启动失败",
            "start_failed": "启动失败",
            "broadband_note_prefix": "宽带测速使用 Speedtest.net 节点；开启仅国内节点后会强制选择国内候选节点，每阶段 ",
            "transfer_note_prefix": "浏览器与本机服务间并发下载/上传 ",
            "seconds_unit": " 秒，实时显示速率。",
            "seconds_short": "秒",
            "save_settings_success": "设置已保存",
            "save_settings_fail": "设置保存失败",
            "refresh_failed": "刷新失败",
            "load_failed": "加载失败",
            "real_time_rate_disabled": "已关闭实时刷新",
            "sampling_since": "应用流量趋势图开始采样于",
            "traffic_sampling_disabled": "流量趋势采样未启用，仅显示当前累计计数",
            "waiting_for_first_sample": "应用流量趋势图等待首次采样",
            "data_insufficient": "数据不足，等待采样",
            "no_app_data": "无应用流量数据",
            "no_ranking_data": "当前区间暂无排行数据",
            "unknown_app": "未知应用",
            "domestic_node": "国内节点",
            "target_node": "目标节点",
            "status_col": "状态",
            "latency_col": "延迟",
            "iface_col": "接口",
            "ipv4_col": "IPv4 地址",
            "ipv6_col": "IPv6 地址",
            "min_rtt": "最小",
            "avg_rtt": "平均",
            "max_rtt": "最大",
            "latency_jitter": "抖动",
            "duration": "耗时",
            "total": "合计",
            "download_data": "下载数据",
            "upload_data": "上传数据",
            "current_rate": "当前速率",
            "interval_avg": "区间平均",
            "interval_peak": "区间峰值",
            "interval_total": "区间总量",
            "latest": "最新",
            "rx_total": "下行总计",
            "tx_total": "上行总计",
            "app_traffic": "应用流量",
            "samples": "采样点",
            "avg_interval": "平均间隔",
            "first_sample": "首个采样",
            "last_sample": "最新采样",
            "rx_delta": "下行增量",
            "tx_delta": "上行增量",
            "current_total": "当前总量",
            "history_title": "历史记录",
            "settings_btn": "设置",
            "broadband_btn": "宽带测速",
            "transfer_btn": "传输测速",
            "lan_devices_btn": "局域网设备",
            "refresh_btn": "刷新",
            "manual_check_btn": "手动检测",
            "start_test_btn": "开始测速",
            "save_btn": "保存",
            "close_btn": "关闭",
            "page_title": "NETWATCH // 网络诊断仪表盘",
            "website_latency_chk": "网站延迟检测",
            "domestic_sites": "国内网站",
            "global_sites": "国际网站",
            "identity_egress_title": "网络身份与出口 IP",
            "proxy_env": "代理环境",
            "default_gateway": "默认网关",
            "platform_connectivity": "平台连通性",
            "domestic_ipv4": "国内 IPv4",
            "domestic_ipv6": "国内 IPv6",
            "domestic_egress_title": "国内出口归属",
            "global_egress_title": "国际出口归属",
            "trace_title": "路由追踪",
            "trace_window_title": "详细跳点",
            "nic_realtime_title": "网卡实时速率",
            "nic_detail_title": "网卡详情",
            "app_traffic_title": "应用流量趋势",
            "settings_window_title": "设置",
            "broadband_window_title": "宽带测速",
            "transfer_window_title": "传输测速",
            "broadband_speedtest": "宽带测速",
            "domestic_nodes_only": "仅使用国内测速节点",
            "nic_realtime_monitor": "网卡实时监测",
            "enable_nic_realtime": "启用网卡实时刷新",
            "nic_realtime_interval": "实时刷新间隔",
            "background_monitor": "后台检测与通知",
            "enable_background_monitor": "启用后台检测",
            "background_monitor_interval": "完整检测间隔",
            "notification_scope": "通知范围",
            "notification_settings": "通知设置",
            "enable_notifications": "启用通知",
            "notification_channels": "通知渠道",
            "client_notification": "客户端通知",
            "notify_abnormal_traffic": "异常流量通知",
            "abnormal_traffic_threshold": "异常流量阈值",
            "notify_egress_change": "国内外出口 IP 变动通知",
            "notify_connectivity_change": "全球互联连接/断开通知",
            "notify_lan_device_change": "局域网设备上线/下线通知",
            "lan_device_offline_after": "离线判定时间",
            "lan_device_online_after": "上线判定时间",
            "lan_device_offline_notify_delay": "离线通知延迟",
            "lan_device_online_notify_delay": "上线通知延迟",
            "bark_enabled": "启用 Bark 推送",
            "bark_server_url": "Bark 服务地址",
            "bark_device_key": "Bark Device Key",
            "bark_group": "Bark 分组",
            "test_bark_notification": "测试 Bark 推送",
            "dnd_settings": "免打扰",
            "dnd_enabled": "启用免打扰",
            "dnd_start": "免打扰开始",
            "dnd_end": "免打扰结束",
            "dnd_note": "免打扰时段内，所有推送通知（Bark 和客户端）将被静默。",
            "scheduled_notify": "定时通知",
            "scheduled_notify_enabled": "启用定时通知",
            "scheduled_notify_time": "每日通知时间",
            "scheduled_notify_note": "每天在指定时间发送网络状态摘要通知，包含出口 IP、全球互联状态和局域网设备统计。",
            "lan_max_check_attempts": "离线确认次数",
            "lan_notify_cooldown_sec": "通知冷却时间",
            "lan_flapping_threshold": "抖动抑制阈值",
            "lan_flapping_window_sec": "抖动检测窗口",
            "lan_device_auto_remove_days": "自动清理离线设备",
            "lan_detection_note": "离线确认次数：连续未检测到设备多少次后才判定为离线，避免误报。通知冷却：同一设备两次通知之间的最短间隔。抖动抑制：在滑动窗口内状态变化超过阈值后，暂停该设备通知。自动清理：离线超过指定天数的设备将被自动移除，已标记和已忽略的设备不受影响。",
            "device_col": "设备",
            "network_info_col": "网络信息",
            "last_seen_col": "最后在线",
            "action_col": "操作",
            "scan_btn": "扫描",
            "background_monitor_note": "完整检测间隔控制后台主动检测网络身份、出口、互联状态和局域网设备扫描；网卡连接/断开使用本地轻量状态检测，会更快更新。",
            "lan_device_list": "局域网设备",
            "lan_monitor_note": "完整后台检测间隔在首页设置中配置；这里仅控制局域网设备通知和判定时间。网卡连接/断开使用本地轻量状态检测，会及时更新并只发送网卡级通知。",
            "traffic_analysis_settings": "流量分析设置",
            "enable_traffic_analysis": "启用流量分析",
            "sampling_control": "采样控制",
            "global_sampling_interval": "全局采样间隔",
            "per_app_sampling": "按应用采样",
            "traffic_page_subtitle": "应用流量分析",
            "back_to_dashboard": "返回仪表盘",
            "app_list_title": "应用列表",
            "search_app_placeholder": "搜索应用",
            "sort_total_desc": "总流量降序",
            "sort_rx_desc": "下行降序",
            "sort_tx_desc": "上行降序",
            "sort_name_asc": "名称升序",
            "range_1_min": "1 分钟",
            "range_5_min": "5 分钟",
            "range_15_min": "15 分钟",
            "range_1_h": "1 小时",
            "range_6_h": "6 小时",
            "range_24_h": "24 小时",
            "range_all": "全部",
            "label_auto": "自动标签",
            "label_3": "每 3 个点",
            "label_5": "每 5 个点",
            "label_10": "每 10 个点",
            "label_20": "每 20 个点",
            "toggle_theme_title": "切换深浅模式",
            "toggle_lang_title": "中文/English",
            "trace_placeholder": "如 github.com",
            "trace_btn": "追踪",
            "trace_initial_hint": "输入目标后开始路径诊断",
            "trace_details_btn": "查看详细跳点",
            "trace_window_note": "显示完整跳点列表与时延信息。",
            "trace_not_started": "尚未开始追踪",
            "waiting_for_sample": "等待采样",
            "waiting_data": "等待数据",
            "traffic_analysis_btn": "流量分析",
            "stage": "阶段",
            "progress": "进度",
            "speedtest_node": "测速节点",
            "node": "节点",
            "provider": "运营商",
            "region": "地区",
            "source": "来源",
            "stage_duration": "阶段耗时",
            "select_node": "选节点",
            "total_duration": "总耗时",
            "failure_info": "失败信息",
            "reason": "原因",
            "rtt_stats": "RTT 统计",
            "current_transfer": "本次传输",
            "settings_note": "统一调整网卡实时监测和后续可配置项。",
            "domestic_nodes_note": "开启后将强制筛选中国大陆境内的运营商节点，适用于存在全局代理但需要测试真实物理带宽的场景。",
            "nic_realtime_note": "这里显示的是宿主机该网卡的总出入流量，浏览器、微信、代理软件、后台应用都会计入。",
            "traffic_settings_note": "配置流量趋势采样、刷新间隔和单独应用采样频率。",
            "per_app_sampling_note": "对特定应用设置独立的采样频率，覆盖全局间隔。",
            "traffic_page_title": "NETWATCH // 应用流量分析",
            "hide_idle": "隐藏空闲",
            "interval_ranking": "区间流量排行",
            "label_density_title": "趋势图时间轴标签密度",
            "live_refresh": "实时刷新",
            "live_interval_title": "实时刷新间隔",
            "interface_counters": "接口计数",
            "interval_samples": "区间采样",
            "connected": "已连接",
            "disconnected": "未连接",
            "connecting": "连接中",
            "disconnecting": "断开中",
            "disabled": "已禁用",
            "unavailable": "不可用",
            "wired": "有线",
            "internet_full": "已联网",
            "internet_limited": "受限",
            "internet_portal": "需要登录认证",
            "internet_none": "无法访问外网",
            "sdk_status_error": "SDK 状态异常",
            "proxy_detected": "存在代理环境",
            "global_egress_detected": "获取到境外出口",
            "no_proxy": "无代理",
            "unknown_status": "状态不明",
            "connection_failed": "连接失败",
            "no_target_nic": "未找到目标网卡",
            "querying": "查询中",
            "queried_at": "查询于",
            "sampled_at": "采样于",
            "sampling": "采样中",
            "sampling_failed": "采样失败",
            "not_detected": "未检测到",
            "high_port_reachable": "高位端口可达",
            "high_port_blocked": "高位端口疑似受限",
            "probe_closed": "探针关闭",
            "high_port_not_checked": "高位端口未检测",
            "high_port_check_failed": "高位端口检测失败",
            "no_monitored_nics": "未配置监控网卡",
            "rx": "下行",
            "tx": "上行",
            "cumulative": "累计",
            "trace_no_hops": "未返回可用跳点",
            "timeout": "超时",
            "geo_lookup_pending": "归属地查询中",
            "target": "目标",
            "tool": "工具",
            "tracing": "追踪中",
            "hops": "跳",
            "request_failed": "请求失败",
            "collecting_trace": "正在采集路径信息...",
            "counter_reset": "计数器重置",
            "sample_gap": "采样断档",
            "rate_spike": "速率突增",
            "peak": "峰值",
            "rx_packets": "收包",
            "tx_packets": "发包",
            "rx_dropped": "收包丢弃",
            "tx_dropped": "发包丢弃",
            "containers": "容器",
            "follow_global": "跟随全局",
            "traffic_settings_saved": "流量设置已保存",
            "label_density_save_failed": "标签密度保存失败",
            "started_broadband": "正在启动宽带测速任务",
            "broadband_start_failed": "宽带测速启动失败",
            "latency_sampling": "延迟采样中",
            "downloading": "正在下载",
            "uploading": "正在上传",
            "starting_transfer": "正在启动网页到本机传输测速",
            "speedtest_start_failed": "测速启动失败",
            "transfer_note_desc": "持续测量浏览器到本机服务的下载、上传、延迟和抖动。",
            "checking": "检测中",
            "check_failed": "检测失败",
            "no_history": "暂无记录",
            "sec_1": "1 秒",
            "sec_2": "2 秒",
            "sec_3": "3 秒",
            "sec_5": "5 秒",
            "sec_10": "10 秒",
            "sec_30": "30 秒",
            "sec_60": "60 秒",
            "sec_120": "120 秒",
            "sec_300": "300 秒"
        },
        "en": {
            "ok": "OK",
            "down": "Down",
            "degraded": "Degraded",
            "unknown": "Unknown",
            "starting": "Starting",
            "latency": "Latency",
            "download": "Download",
            "upload": "Upload",
            "finalizing": "Finalizing",
            "complete": "Complete",
            "canceled": "Canceled",
            "error": "Error",
            "failed": "Failed",
            "idle": "Idle",
            "standby": "Standby",
            "waiting": "Waiting",
            "loading": "Loading...",
            "searching": "Searching...",
            "no_data": "No data",
            "no_results": "No results",
            "no_records": "No records",
            "preparing": "Preparing",
            "dl_speedtest": "Download Test",
            "ul_speedtest": "Upload Test",
            "speedtest_complete": "Speed test complete",
            "speedtest_stopped": "Speed test stopped",
            "speedtest_failed": "Speed test failed",
            "manual_stop": "Manually stopped",
            "transfer_done": "Browser-to-server transfer complete",
            "transfer_stopped": "Browser-to-server transfer stopped",
            "transfer_start_failed": "Browser-to-server start failed",
            "start_failed": "Start failed",
            "broadband_note_prefix": "Using Speedtest.net; domestic-only forces domestic nodes. Each phase ",
            "transfer_note_prefix": "Concurrent download/upload between browser and server ",
            "seconds_unit": " sec, real-time display.",
            "seconds_short": "sec",
            "save_settings_success": "Settings saved",
            "save_settings_fail": "Failed to save settings",
            "refresh_failed": "Refresh failed",
            "load_failed": "Load failed",
            "real_time_rate_disabled": "Real-time refresh disabled",
            "sampling_since": "App traffic trend started at",
            "traffic_sampling_disabled": "Traffic sampling disabled, showing cumulative counts only",
            "waiting_for_first_sample": "Waiting for first sample",
            "data_insufficient": "Insufficient data, waiting for samples",
            "no_app_data": "No app traffic data",
            "no_ranking_data": "No ranking data for this range",
            "unknown_app": "Unknown app",
            "domestic_node": "Domestic node",
            "target_node": "Target",
            "status_col": "Status",
            "latency_col": "Latency",
            "iface_col": "Interface",
            "ipv4_col": "IPv4 Address",
            "ipv6_col": "IPv6 Address",
            "min_rtt": "Min",
            "avg_rtt": "Avg",
            "max_rtt": "Max",
            "latency_jitter": "Jitter",
            "duration": "Duration",
            "total": "Total",
            "download_data": "Download",
            "upload_data": "Upload",
            "current_rate": "Current Rate",
            "interval_avg": "Avg Rate",
            "interval_peak": "Peak Rate",
            "interval_total": "Total Volume",
            "latest": "Latest",
            "rx_total": "RX Total",
            "tx_total": "TX Total",
            "app_traffic": "App Traffic",
            "samples": "Samples",
            "avg_interval": "Avg Interval",
            "first_sample": "First Sample",
            "last_sample": "Last Sample",
            "rx_delta": "RX Delta",
            "tx_delta": "TX Delta",
            "current_total": "Current Total",
            "history_title": "History",
            "settings_btn": "Settings",
            "broadband_btn": "Broadband",
            "transfer_btn": "Transfer",
            "lan_devices_btn": "LAN Devices",
            "refresh_btn": "Refresh",
            "manual_check_btn": "Check Now",
            "start_test_btn": "Start Test",
            "save_btn": "Save",
            "close_btn": "Close",
            "page_title": "NETWATCH // Network Diagnostics Dashboard",
            "website_latency_chk": "Website Latency",
            "domestic_sites": "Domestic Sites",
            "global_sites": "Global Sites",
            "identity_egress_title": "Network Identity & Egress IP",
            "proxy_env": "Proxy Environment",
            "default_gateway": "Default Gateway",
            "platform_connectivity": "Platform Connectivity",
            "domestic_ipv4": "Domestic IPv4",
            "domestic_ipv6": "Domestic IPv6",
            "domestic_egress_title": "Domestic Egress Location",
            "global_egress_title": "Global Egress Location",
            "trace_title": "Route Trace",
            "trace_window_title": "Trace Hops",
            "nic_realtime_title": "NIC Real-Time Rate",
            "nic_detail_title": "NIC Details",
            "app_traffic_title": "App Traffic Trend",
            "settings_window_title": "Settings",
            "broadband_window_title": "Broadband Speed Test",
            "transfer_window_title": "Transfer Speed Test",
            "broadband_speedtest": "Broadband Speed Test",
            "domestic_nodes_only": "Domestic speedtest nodes only",
            "nic_realtime_monitor": "NIC Real-Time Monitor",
            "enable_nic_realtime": "Enable NIC real-time refresh",
            "nic_realtime_interval": "Real-time refresh interval",
            "background_monitor": "Background Detection & Notifications",
            "enable_background_monitor": "Enable background detection",
            "background_monitor_interval": "Full check interval",
            "notification_scope": "Notification Scope",
            "notification_settings": "Notification Settings",
            "enable_notifications": "Enable notifications",
            "notification_channels": "Notification Channels",
            "client_notification": "Client notification",
            "notify_abnormal_traffic": "Abnormal traffic notifications",
            "abnormal_traffic_threshold": "Abnormal traffic threshold",
            "notify_egress_change": "Domestic/global egress IP change notifications",
            "notify_connectivity_change": "Global connectivity connect/disconnect notifications",
            "notify_lan_device_change": "LAN device online/offline notifications",
            "lan_device_offline_after": "Offline detection time",
            "lan_device_online_after": "Online detection time",
            "lan_device_offline_notify_delay": "Offline alert delay",
            "lan_device_online_notify_delay": "Online alert delay",
            "bark_enabled": "Enable Bark push",
            "bark_server_url": "Bark server URL",
            "bark_device_key": "Bark Device Key",
            "bark_group": "Bark group",
            "test_bark_notification": "Test Bark push",
            "dnd_settings": "Do Not Disturb",
            "dnd_enabled": "Enable DND",
            "dnd_start": "DND start",
            "dnd_end": "DND end",
            "dnd_note": "During DND hours, all push notifications (Bark and client) are silenced.",
            "scheduled_notify": "Scheduled Notification",
            "scheduled_notify_enabled": "Enable scheduled notification",
            "scheduled_notify_time": "Daily notification time",
            "scheduled_notify_note": "Sends a daily network status summary at the specified time, including egress IP, global connectivity, and LAN device stats.",
            "lan_max_check_attempts": "Offline confirmation count",
            "lan_notify_cooldown_sec": "Notification cooldown",
            "lan_flapping_threshold": "Flapping suppression threshold",
            "lan_flapping_window_sec": "Flapping detection window",
            "lan_device_auto_remove_days": "Auto-remove offline devices",
            "lan_detection_note": "Offline confirmation: number of consecutive misses before marking a device offline. Cooldown: minimum interval between notifications for the same device. Flapping suppression: pauses notifications when state changes exceed the threshold within the sliding window. Auto-remove: devices offline longer than the configured days are automatically cleaned up; pinned and ignored devices are preserved.",
            "device_col": "Device",
            "network_info_col": "Network Info",
            "last_seen_col": "Last Seen",
            "action_col": "Action",
            "scan_btn": "Scan",
            "background_monitor_note": "The full check interval controls background network identity, egress, connectivity, and LAN device scans. NIC connect/disconnect uses lightweight local state checks and updates faster.",
            "lan_device_list": "LAN Devices",
            "lan_monitor_note": "The full background check interval is configured on the dashboard settings page. This panel only controls LAN device notifications and detection timing. NIC connect/disconnect uses lightweight local state checks and sends only NIC-level notifications.",
            "traffic_analysis_settings": "Traffic Analysis Settings",
            "enable_traffic_analysis": "Enable Traffic Analysis",
            "sampling_control": "Sampling Control",
            "global_sampling_interval": "Global Sampling Interval",
            "per_app_sampling": "Per-App Sampling",
            "traffic_page_subtitle": "App Traffic Analysis",
            "back_to_dashboard": "Back to Dashboard",
            "app_list_title": "Apps",
            "search_app_placeholder": "Search apps",
            "sort_total_desc": "Total traffic desc",
            "sort_rx_desc": "Download desc",
            "sort_tx_desc": "Upload desc",
            "sort_name_asc": "Name asc",
            "range_1_min": "1 min",
            "range_5_min": "5 min",
            "range_15_min": "15 min",
            "range_1_h": "1 hour",
            "range_6_h": "6 hours",
            "range_24_h": "24 hours",
            "range_all": "All",
            "label_auto": "Auto labels",
            "label_3": "Every 3 points",
            "label_5": "Every 5 points",
            "label_10": "Every 10 points",
            "label_20": "Every 20 points",
            "toggle_theme_title": "Toggle theme",
            "toggle_lang_title": "Chinese/English",
            "trace_placeholder": "e.g. github.com",
            "trace_btn": "Trace",
            "trace_initial_hint": "Enter a target to start path diagnostics",
            "trace_details_btn": "View Hops",
            "trace_window_note": "Shows the full hop list and latency details.",
            "trace_not_started": "Trace not started",
            "waiting_for_sample": "Waiting for samples",
            "waiting_data": "Waiting for data",
            "traffic_analysis_btn": "Traffic Analysis",
            "stage": "Stage",
            "progress": "Progress",
            "speedtest_node": "Speedtest Node",
            "node": "Node",
            "provider": "Provider",
            "region": "Region",
            "source": "Source",
            "stage_duration": "Stage Duration",
            "select_node": "Node Select",
            "total_duration": "Total Duration",
            "failure_info": "Failure Info",
            "reason": "Reason",
            "rtt_stats": "RTT Stats",
            "current_transfer": "This Transfer",
            "settings_note": "Adjust NIC real-time monitoring and related options.",
            "domestic_nodes_note": "Forces mainland China carrier nodes, useful when a global proxy is present but you need to test the physical line.",
            "nic_realtime_note": "Shows total host traffic for the monitored NIC. Browsers, chat apps, proxy tools, and background apps are all included.",
            "traffic_settings_note": "Configure traffic trend sampling, refresh intervals, and per-app sampling frequency.",
            "per_app_sampling_note": "Set independent sampling frequency for specific apps, overriding the global interval.",
            "traffic_page_title": "NETWATCH // App Traffic Analysis",
            "hide_idle": "Hide Idle",
            "interval_ranking": "Interval Ranking",
            "label_density_title": "Trend chart time label density",
            "live_refresh": "Live Refresh",
            "live_interval_title": "Live refresh interval",
            "interface_counters": "Interface Counters",
            "interval_samples": "Interval Samples",
            "connected": "Connected",
            "disconnected": "Disconnected",
            "connecting": "Connecting",
            "disconnecting": "Disconnecting",
            "disabled": "Disabled",
            "unavailable": "Unavailable",
            "wired": "Wired",
            "internet_full": "Online",
            "internet_limited": "Limited",
            "internet_portal": "Portal login required",
            "internet_none": "No internet access",
            "sdk_status_error": "SDK status error",
            "proxy_detected": "Proxy environment detected",
            "global_egress_detected": "Global egress detected",
            "no_proxy": "No proxy",
            "unknown_status": "Unknown status",
            "connection_failed": "Connection failed",
            "no_target_nic": "No target NIC found",
            "querying": "Querying",
            "queried_at": "Queried at",
            "sampled_at": "Sampled at",
            "sampling": "Sampling",
            "sampling_failed": "Sampling failed",
            "not_detected": "Not detected",
            "high_port_reachable": "High port reachable",
            "high_port_blocked": "High port likely restricted",
            "probe_closed": "Probe closed",
            "high_port_not_checked": "High port not checked",
            "high_port_check_failed": "High port check failed",
            "no_monitored_nics": "No monitored NICs",
            "rx": "Download",
            "tx": "Upload",
            "cumulative": "Total",
            "trace_no_hops": "No usable hops returned",
            "timeout": "Timeout",
            "geo_lookup_pending": "Location lookup pending",
            "target": "Target",
            "tool": "Tool",
            "tracing": "Tracing",
            "hops": "hops",
            "request_failed": "Request failed",
            "collecting_trace": "Collecting path information...",
            "counter_reset": "Counter reset",
            "sample_gap": "Sample gap",
            "rate_spike": "Rate spike",
            "peak": "Peak",
            "rx_packets": "RX packets",
            "tx_packets": "TX packets",
            "rx_dropped": "RX dropped",
            "tx_dropped": "TX dropped",
            "containers": "Containers",
            "follow_global": "Follow global",
            "traffic_settings_saved": "Traffic settings saved",
            "label_density_save_failed": "Failed to save label density",
            "started_broadband": "Starting broadband speed test",
            "broadband_start_failed": "Failed to start broadband speed test",
            "latency_sampling": "Sampling latency",
            "downloading": "Downloading",
            "uploading": "Uploading",
            "starting_transfer": "Starting browser-to-server transfer test",
            "speedtest_start_failed": "Failed to start speed test",
            "transfer_note_desc": "Continuously measures browser-to-server download, upload, latency, and jitter.",
            "checking": "Checking",
            "check_failed": "Check failed",
            "no_history": "No history",
            "sec_1": "1 sec",
            "sec_2": "2 sec",
            "sec_3": "3 sec",
            "sec_5": "5 sec",
            "sec_10": "10 sec",
            "sec_30": "30 sec",
            "sec_60": "60 sec",
            "sec_120": "120 sec",
            "sec_300": "300 sec"
        }
    };

    function getLang() {
        return localStorage.getItem('netwatch_lang') || 'zh-CN';
    }

    function setLang(lang) {
        localStorage.setItem('netwatch_lang', lang);
        document.documentElement.setAttribute('lang', lang);
        applyI18n();
    }

    function translate(key) {
        var lang = getLang();
        var val = dict[lang] && dict[lang][key];
        return val !== undefined ? val : key;
    }

    window.__ = translate;

    function syncCustomSelect(select) {
        var wrapper = select && select.__customSelect;
        if (!wrapper) return;
        var valueNode = wrapper.querySelector('.custom-select-value');
        var menu = wrapper.__customSelectMenu || wrapper.querySelector('.custom-select-menu');
        var previousOptions = wrapper.__optionSignature || '';
        var nextOptions = Array.prototype.map.call(select.options, function (option) {
            return option.value + '\u0000' + option.textContent;
        }).join('\u0001');
        if (menu && previousOptions !== nextOptions) {
            wrapper.__optionSignature = nextOptions;
            menu.innerHTML = '';
            Array.prototype.forEach.call(select.options, function (option) {
                var btn = document.createElement('button');
                btn.type = 'button';
                btn.className = 'custom-select-option';
                btn.dataset.value = option.value;
                btn.textContent = option.textContent;
                btn.addEventListener('click', function () {
                    select.value = option.value;
                    select.dispatchEvent(new Event('change', { bubbles: true }));
                    closeCustomSelects();
                });
                menu.appendChild(btn);
            });
        }
        var selected = select.options[select.selectedIndex];
        if (valueNode) {
            valueNode.textContent = selected ? selected.textContent : '';
        }
        if (menu) {
            Array.prototype.forEach.call(menu.querySelectorAll('.custom-select-option'), function (btn) {
                btn.classList.toggle('active', btn.dataset.value === select.value);
            });
        }
        wrapper.classList.toggle('disabled', select.disabled);
        if (wrapper.classList.contains('open')) {
            positionCustomSelectMenu(wrapper);
        }
    }

    function positionCustomSelectMenu(wrapper) {
        var trigger = wrapper && wrapper.querySelector('.custom-select-trigger');
        var menu = wrapper && (wrapper.__customSelectMenu || wrapper.querySelector('.custom-select-menu'));
        if (!trigger || !menu) return;
        var rect = trigger.getBoundingClientRect();
        var gap = 6;
        var below = window.innerHeight - rect.bottom - gap - 12;
        var above = rect.top - gap - 12;
        var openAbove = below < 120 && above > below;
        var maxHeight = Math.min(240, Math.max(120, openAbove ? above : below));
        menu.style.left = Math.max(8, Math.min(rect.left, window.innerWidth - rect.width - 8)) + 'px';
        menu.style.top = (openAbove ? rect.top : rect.bottom + gap) + 'px';
        if (openAbove) {
            menu.style.transformOrigin = 'bottom';
            menu.style.setProperty('--custom-select-menu-y', 'calc(-100% - 6px)');
        } else {
            menu.style.transformOrigin = 'top';
            menu.style.setProperty('--custom-select-menu-y', '0px');
        }
        menu.style.width = rect.width + 'px';
        menu.style.maxHeight = maxHeight + 'px';
    }

    function closeCustomSelects(except) {
        document.querySelectorAll('.custom-select.open').forEach(function (wrapper) {
            if (wrapper !== except) {
                wrapper.classList.remove('open');
                var menu = wrapper.__customSelectMenu || wrapper.querySelector('.custom-select-menu');
                if (menu) menu.classList.remove('open');
            }
        });
        document.querySelectorAll('.custom-select-menu.open').forEach(function (menu) {
            if (!except || menu.__customSelectWrapper !== except) {
                menu.classList.remove('open');
            }
        });
    }

    function enhanceSelect(select) {
        if (!select || select.__customSelect || select.dataset.nativeSelect === 'true') return;
        var parent = select.parentNode;
        if (!parent) return;
        var originalClasses = Array.prototype.slice.call(select.classList).filter(function (name) {
            return name !== 'custom-select-native';
        });
        var wrapper = document.createElement('div');
        wrapper.className = ['custom-select'].concat(originalClasses).join(' ');
        var trigger = document.createElement('button');
        trigger.type = 'button';
        trigger.className = 'custom-select-trigger';
        var value = document.createElement('span');
        value.className = 'custom-select-value';
        trigger.appendChild(value);
        var menu = document.createElement('div');
        menu.className = 'custom-select-menu';
        menu.setAttribute('data-custom-select-menu', 'true');
        trigger.addEventListener('click', function () {
            if (select.disabled) return;
            var willOpen = !wrapper.classList.contains('open');
            closeCustomSelects(wrapper);
            wrapper.classList.toggle('open', willOpen);
            menu.classList.toggle('open', willOpen);
            if (willOpen) {
                positionCustomSelectMenu(wrapper);
            }
        });
        parent.insertBefore(wrapper, select);
        wrapper.appendChild(select);
        wrapper.appendChild(trigger);
        document.body.appendChild(menu);
        select.classList.add('custom-select-native');
        select.setAttribute('aria-hidden', 'true');
        select.tabIndex = -1;
        select.__customSelect = wrapper;
        menu.__customSelectWrapper = wrapper;
        wrapper.__customSelectMenu = menu;
        wrapper.__optionSignature = '';
        select.addEventListener('change', function () {
            syncCustomSelect(select);
        });
        syncCustomSelect(select);
    }

    function enhanceSelects(root) {
        root = root || document;
        document.querySelectorAll('[data-custom-select-menu="true"]').forEach(function (menu) {
            if (!menu.__customSelectWrapper || !document.documentElement.contains(menu.__customSelectWrapper)) {
                menu.remove();
            }
        });
        root.querySelectorAll('select').forEach(enhanceSelect);
    }

    function applyI18n(root) {
        root = root || document;
        var lang = getLang();
        document.documentElement.setAttribute('lang', lang);
        root.querySelectorAll('[data-i18n]').forEach(function (el) {
            var key = el.getAttribute('data-i18n');
            if (key) {
                el.textContent = window.__(key);
            }
        });
        root.querySelectorAll('[data-i18n-placeholder]').forEach(function (el) {
            var key = el.getAttribute('data-i18n-placeholder');
            if (key) {
                el.placeholder = window.__(key);
            }
        });
        root.querySelectorAll('[data-i18n-title]').forEach(function (el) {
            var key = el.getAttribute('data-i18n-title');
            if (key) {
                el.title = window.__(key);
            }
        });
        root.querySelectorAll('select').forEach(function (select) {
            var wrapper = select.__customSelect;
            if (!wrapper) return;
            var menu = wrapper.__customSelectMenu || wrapper.querySelector('.custom-select-menu');
            if (!menu) return;
            Array.prototype.forEach.call(select.options, function (option, idx) {
                var btn = menu.children[idx];
                if (btn) btn.textContent = option.textContent;
            });
            syncCustomSelect(select);
        });
    }

    window.initI18n = function () {
        var lang = getLang();
        setLang(lang);
        enhanceSelects();
        var toggle = document.getElementById('lang-toggle');
        if (toggle) {
            toggle.addEventListener('click', function () {
                var next = getLang() === 'zh-CN' ? 'en' : 'zh-CN';
                setLang(next);
            });
        }
    };

    window.applyI18n = applyI18n;
    window.enhanceSelects = enhanceSelects;
    window.syncCustomSelect = syncCustomSelect;

    document.addEventListener('click', function (ev) {
        var checkboxLabel = ev.target.closest('label');
        if (checkboxLabel && ev.target.tagName !== 'INPUT' && checkboxLabel.querySelector('input[type="checkbox"]')) {
            ev.preventDefault();
            return;
        }
        if (!ev.target.closest('.custom-select') && !ev.target.closest('.custom-select-menu')) {
            closeCustomSelects();
        }
    });

    document.addEventListener('keydown', function (ev) {
        if (ev.key === 'Escape') {
            closeCustomSelects();
        }
    });

    window.addEventListener('resize', function () {
        document.querySelectorAll('.custom-select.open').forEach(positionCustomSelectMenu);
    });

    window.addEventListener('scroll', function () {
        document.querySelectorAll('.custom-select.open').forEach(positionCustomSelectMenu);
    }, true);

    if (document.readyState === 'loading') {
        document.addEventListener('DOMContentLoaded', window.initI18n);
    } else {
        window.initI18n();
    }
})();
