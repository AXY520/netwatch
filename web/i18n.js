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
            "traffic_download": "下载",
            "traffic_upload": "上传",
            "finalizing": "整理结果",
            "complete": "已完成",
            "canceled": "已停止",
            "error": "错误",
            "failed": "失败",
            "idle": "待启动",
            "standby": "等待测速开始",
            "waiting": "等待中",
            "loading": "加载中",
            "scanning": "扫描中",
            "no_data": "无数据",
            "no_results": "暂无检测结果",
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
            "lan_device_search_placeholder": "搜索设备",
            "network_config_preflight": "预检配置",
            "network_config_preview": "配置预览",
            "network_config_preflight_auto": "自动获取模式无需 IP 占用预检",
            "latency_col": "延迟",
            "iface_col": "接口",
            "ipv4_col": "IPv4 地址",
            "ipv6_col": "IPv6 地址",
            "host_ports_title": "端口占用",
            "collapse_panel": "折叠板块",
            "expand_panel": "展开板块",
            "network_config_btn": "修改配置",
            "network_config_title": "网络配置",
            "network_config_note": "网卡 / 网桥 / DNS 同窗；改 IP 与网桥 3 分钟确认，改 DNS 60 秒确认。",
            "network_config_device": "网卡",
            "network_config_method": "配置方式",
            "network_config_method_auto": "自动获取",
            "network_config_method_manual": "手动配置",
            "network_config_address": "IPv4/CIDR",
            "network_config_gateway": "网关",
            "network_config_dns": "DNS",
            "network_config_check_ip": "检测 IP 占用",
            "network_config_apply": "应用配置",
            "network_config_confirm": "确认网络可用",
            "network_config_rollback": "立即回滚",
            "network_config_disabled": "网卡配置未启用，请设置 NETWORK_CONFIG_ENABLED=true",
            "network_config_backend_old": "当前后端不包含网卡配置接口，请重启服务或重新部署新版后端",
            "network_config_no_device": "没有可配置的已连接网卡",
            "network_config_ready": "请选择网卡并填写配置",
            "network_config_required": "请填写网卡、IPv4/CIDR、网关和 DNS",
            "network_config_check_required": "请先选择网卡并填写 IPv4 地址",
            "network_config_ip_invalid": "IPv4 地址不合法，请输入类似 192.168.1.10/24 的地址",
            "network_config_checking_ip": "正在检测 IP 是否被占用...",
            "network_config_ip_available": "该 IP 暂未发现占用，可以尝试使用",
            "network_config_ip_occupied": "该 IP 疑似已被占用",
            "network_config_apply_confirm": "即将修改服务器网卡配置。应用后 3 分钟内必须确认，否则自动回滚。继续？",
            "network_config_applying": "正在应用配置...",
            "network_config_pending": "配置已应用，等待确认，自动回滚时间",
            "network_config_pending_active": "网卡配置待确认，自动回滚倒计时",
            "network_config_failed": "网卡配置失败",
            "network_config_no_pending": "没有待确认的网卡配置变更",
            "network_config_confirmed": "已确认网络可用，不再自动回滚",
            "network_config_rolled_back": "已回滚网卡配置",
            "host_bridge_title": "网桥",
            "host_bridge_no_ethernet": "无可用有线网卡（Wi-Fi 不支持创建网桥）",
            "proxy_tun_title": "代理",
            "host_bridge_name": "网桥名",
            "host_bridge_method": "地址方式",
            "host_bridge_method_inherit": "继承当前网卡",
            "host_bridge_method_auto": "自动获取",
            "host_bridge_method_manual": "手动配置",
            "host_bridge_create": "创建网桥",
            "host_bridge_confirm": "确认网桥可用",
            "host_bridge_rollback": "回滚网桥",
            "host_bridge_dissolve": "拆除网桥",
            "host_bridge_create_confirm": "即将把该有线网卡加入网桥，主机地址会迁移到网桥上。创建后 3 分钟内必须确认，否则自动回滚。继续？",
            "host_bridge_dissolve_confirm": "拆除网桥将恢复原网卡连接，已接入该网桥的设备网络会中断。继续？",
            "host_bridge_none": "当前没有托管网桥",
            "host_bridge_for_device": "当前网卡托管网桥",
            "host_bridge_pending": "网桥待确认，自动回滚倒计时",
            "host_bridge_creating": "正在创建网桥...",
            "host_bridge_failed": "网桥操作失败",
            "host_bridge_need_device": "请先选择有线网卡",
            "host_bridge_disabled": "当前环境不支持网桥管理（需要懒猫系统接口或本机 nmcli）",
            "host_bridge_select": "选择网桥",
            "host_bridge_select_need": "请先选择要拆除的网桥",
            "host_bridge_name_required": "请填写网桥名后缀",
            "host_bridge_name_too_long": "网桥名最长 15 字符（含固定前缀 nw-）",
            "host_bridge_name_invalid": "网桥名不合法：字母数字开头，仅允许 . _ -",
            "host_bridge_name_prefix": "网桥名必须以 nw- 开头",
            "host_bridge_preflight_skip": "继承/自动模式无需 IP 占用预检",
            "network_config_tab_ip": "网卡配置",
            "network_config_tab_bridge": "网桥",
            "network_config_tab_dns": "DNS",
            "host_dns_method": "DNS 获取方式",
            "host_dns_method_auto": "自动（跟随 DHCP）",
            "host_dns_method_manual": "手动指定",
            "host_dns_servers": "DNS 服务器",
            "host_dns_apply": "应用 DNS",
            "host_dns_confirm": "确认可用",
            "host_dns_rollback": "立即回滚",
            "host_dns_pending": "DNS 待确认，自动回滚倒计时",
            "host_dns_apply_confirm": "将修改宿主机当前连接的 DNS（经系统 SDK）。解析失败会导致页面打不开，60 秒内未确认将自动回滚。继续？",
            "host_dns_applying": "正在应用 DNS…",
            "host_dns_failed": "DNS 操作失败",
            "host_dns_disabled": "当前环境无法管理 DNS",
            "host_dns_target": "目标连接",
            "host_dns_runtime": "当前生效",
            "port_col": "端口",
            "owner_col": "占用方",
            "listen_addr_col": "监听地址",
            "process_col": "进程",
            "container_col": "容器 / 应用",
            "host_process": "宿主进程",
            "host_owner": "宿主",
            "host_services": "宿主机服务",
            "app_owner": "应用",
            "unknown_process": "未知进程",
            "host_port_detail_title": "端口详情",
            "no_host_ports": "暂无监听端口",
            "port_search_label": "搜索端口",
            "port_search_placeholder": "端口号",
            "port_not_occupied": "当前采样中未发现匹配的端口",
            "port_search_invalid": "请输入 1-5 位数字",
            "min_rtt": "最小",
            "avg_rtt": "平均",
            "max_rtt": "最大",
            "latency_jitter": "抖动",
            "duration": "耗时",
            "total": "合计",
            "download_data": "下载数据",
            "upload_data": "上传数据",
            "current_rate": "最近速率",
            "interval_avg": "区间平均",
            "interval_peak": "区间峰值",
            "interval_total": "区间总量",
            "latest": "最新",
            "rx_total": "上传总计",
            "tx_total": "下载总计",
            "app_traffic": "应用流量",
            "samples": "采样点",
            "avg_interval": "平均间隔",
            "first_sample": "首个采样",
            "last_sample": "最新采样",
            "rx_delta": "上传增量",
            "tx_delta": "下载增量",
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
            "lan_page_title": "NETWATCH // 局域网设备发现",
            "website_latency_chk": "网站延迟检测",
            "domestic_sites": "国内网站",
            "global_sites": "国际网站",
            "identity_egress_title": "网络身份与出口 IP",
            "proxy_env": "代理环境",
            "default_gateway": "默认网关",
            "platform_connectivity": "平台连通性",
            "nat_title": "NAT 类型",
            "confidence": "可信度",
            "confidence_high": "高",
            "confidence_medium": "中",
            "confidence_low": "低",
            "checked_at": "检测于",
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
            "broadband_steps_title": "操作日志",
            "transfer_window_title": "传输测速",
            "broadband_speedtest": "宽带测速",
            "domestic_nodes_only": "仅国内节点",
            "nic_realtime_monitor": "网卡实时刷新",
            "background_monitor": "后台检测",
            "notification_settings": "通知设置",
            "notification_settings_desc": "配置通知触发条件、通知渠道、设备选择和免打扰时段。",
            "notification_settings_btn": "通知设置",
            "notification_triggers": "通知触发条件",
            "enable_notifications": "启用通知",
            "toggle_on": "开",
            "container_control": "容器网络控制",
            "no_containers": "暂无容器",
            "running": "运行中",
            "blocked_internet": "禁外网",
            "blocked_all": "禁全网",
            "block_internet_btn": "禁外网",
            "block_internet_title": "阻断容器的外网访问(保留内网)",

            "unblock_btn": "恢复",
            "container_blocked": "容器网络已封锁",
            "container_unblocked": "容器网络已恢复",
            "operation_failed": "操作失败",
            "client_notification": "客户端通知",
            "notify_abnormal_traffic": "异常流量",
            "notify_egress_change": "出口 IP 变动",
            "notify_connectivity_change": "全球互联断开",
            "notify_lan_device_change": "局域网设备变动",
            "lan_device_offline_after": "离线判定",
            "lan_device_online_after": "上线判定",
            "lan_device_offline_notify_delay": "离线通知延迟",
            "lan_device_online_notify_delay": "上线通知延迟",
            "bark_enabled": "Bark 推送",
            "bark_server_url": "Bark 地址",
            "bark_device_key": "Bark Key",
            "bark_group": "Bark 分组",
            "test_bark_notification": "测试 Bark 推送",
            "test_pushplus": "测试 PushPlus",
            "pushplus_token": "PushPlus Token",
            "pushplus_topic": "PushPlus 群组",
            "dnd_settings": "免打扰",
            "scheduled_notify": "定时通知",
            "notify_content": "通知内容",
            "notify_content_edit": "自定义",
            "notify_content_desc": "自定义通知标题和正文模板，留空则使用默认格式。",
            "notify_template_title": "标题模板",
            "notify_template_body": "正文模板",
            "notify_template_vars": "可用变量：",
            "lan_max_check_attempts": "离线确认",
            "lan_notify_cooldown_sec": "通知冷却",
            "lan_flapping_threshold": "抖动抑制阈值",
            "lan_flapping_window_sec": "抖动检测窗口",
            "lan_device_auto_remove_days": "自动清理",
            "device_col": "设备",
            "network_info_col": "网络信息",
            "last_seen_col": "最后在线",
            "action_col": "操作",
            "scan_btn": "扫描",
            "notification_devices": "通知设备",
            "no_devices_registered": "暂无注册设备",
            "status_online": "在线",
            "status_offline": "离线",
            "status_iface_down": "网卡断开",
            "status_pending": "待确认",
            "status_online_confirming": "上线确认中",
            "status_unknown": "未知",
            "test_notify_sent": "测试通知已发送",
            "test_notify_failed": "测试通知失败",
            "settings_saved": "设置已保存",
            "settings_save_failed": "设置保存失败",
            "lan_load_failed": "局域网设备加载失败",
            "device_mark_updated": "设备标记已更新",
            "device_mark_failed": "设备标记更新失败",
            "test_notify_title": "Netwatch 测试通知",
            "test_notify_body": "客户端通知 API 可用。",
            "traffic_analysis_settings": "流量分析设置",
            "enable_traffic_analysis": "启用流量分析",
            "per_app_sampling": "按应用采样",
            "traffic_page_subtitle": "应用流量分析",
            "back_to_dashboard": "返回仪表盘",
            "app_list_title": "应用列表",
            "search_app_placeholder": "搜索应用",
            "sort_total_desc": "总流量降序",
            "sort_rx_desc": "上传降序",
            "sort_tx_desc": "下载降序",
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
            "trace_stop": "停止",
            "trace_stopped": "已停止",
            "trace_done": "已完成",
            "trace_running_hint": "追踪进行中，可打开跳点窗口或点停止",
            "trace_initial_hint": "输入目标后开始路径诊断",
            "trace_details_btn": "查看详细跳点",
            "trace_window_note": "显示完整跳点列表与时延信息。",
            "trace_not_started": "尚未开始追踪",
            "waiting_for_sample": "等待采样",
            "waiting_data": "等待数据",
            "traffic_analysis_btn": "流量分析",
            "traffic_live_rate": "当前速率",
            "traffic_open_settings_hint": "请在设置中启用流量分析后再查看",
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
            "sampling_off_hint": "采样未启用",
            "not_detected": "未检测到",
            "ipv6_checking": "检测中",
            "ipv6_fully_usable": "IPv6 完全可用",
            "ipv6_outbound_only": "IPv6 仅出站可用",
            "ipv6_address_only": "仅有地址·不可路由",
            "ipv6_no_global": "无公网 IPv6",
            "ipv6_layer_addr": "公网地址",
            "ipv6_layer_outbound": "出站连通",
            "ipv6_layer_https": "HTTPS over IPv6",
            "ipv6_layer_dns": "DNS AAAA 解析",
            "ipv6_view_detail": "查看详情",
            "ipv6_detail_title": "IPv6 真实可用性详情",
            "ipv6_detail_note": "分层检测本机 IPv6 的真实连通性，从地址到应用层逐层确认。",
            "ipv6_detail_conclusion": "综合结论",
            "ipv6_detail_addr_desc": "本机是否拥有公网可路由的 IPv6 地址（排除 fe80 链路本地、fc00 私有地址）。",
            "ipv6_detail_outbound_desc": "能否通过 IPv6 向公网发起 TCP 连接（连接国内大厂 anycast 节点验证）。",
            "ipv6_detail_https_desc": "能否通过 IPv6 完成 HTTPS 应用层访问（访问国内双栈站点验证）。",
            "ipv6_detail_dns_desc": "DNS 能否解析出 AAAA 记录（应用走 IPv6 的前提）。",
            "ipv6_detail_target": "命中目标",
            "ipv6_detail_checked_at": "检测时间",
            "ipv6_renew_title": "IPv6 地址续约",
            "ipv6_renew_note": "当 IPv6 租约过期但地址未更新时，可让系统重新获取。选择网卡后执行，过程通常不会中断现有连接。",
            "ipv6_renew_select_nic": "选择网卡",
            "ipv6_renew_exec": "重新获取 IPv6",
            "ipv6_renew_refresh": "刷新网卡",
            "ipv6_renew_no_nic": "未发现可续约的网卡（需已连接的有线/Wi-Fi 或 nw- 网桥）",
            "ipv6_renew_unavailable": "当前环境不支持（需懒猫系统接口或本机 nmcli）",
            "ipv6_renew_running": "正在续约…",
            "ipv6_renew_ok": "续约成功，已重新获取 IPv6",
            "ipv6_renew_failed": "续约失败",
            "no_monitored_nics": "未配置监控网卡",
            "rx": "下行",
            "tx": "上行",
            "app_traffic_upload": "上传",
            "app_traffic_download": "下载",
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
            "sec_300": "300 秒",
            "confirm_cancel_test": "测速正在进行，确定要关闭吗？",
            "confirm_cancel_title": "取消测速",
            "confirm_cancel_ok": "确定取消",
            "confirm_cancel_keep": "继续测速"
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
            "traffic_download": "Download",
            "traffic_upload": "Upload",
            "finalizing": "Finalizing",
            "complete": "Complete",
            "canceled": "Canceled",
            "error": "Error",
            "failed": "Failed",
            "idle": "Idle",
            "standby": "Standby",
            "waiting": "Waiting",
            "loading": "Loading...",
            "scanning": "Scanning...",
            "no_data": "No data",
            "no_results": "No results",
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
            "lan_device_search_placeholder": "Search devices",
            "network_config_preflight": "Preflight config",
            "network_config_preview": "Config preview",
            "network_config_preflight_auto": "Automatic mode does not need an IP conflict check",
            "latency_col": "Latency",
            "iface_col": "Interface",
            "ipv4_col": "IPv4 Address",
            "ipv6_col": "IPv6 Address",
            "host_ports_title": "Port Usage",
            "collapse_panel": "Collapse section",
            "expand_panel": "Expand section",
            "network_config_btn": "Configure",
            "network_config_title": "Network Config",
            "network_config_note": "IP / bridge / DNS in one window; IP & bridge 3 min confirm, DNS 60s.",
            "network_config_device": "Interface",
            "network_config_method": "Method",
            "network_config_method_auto": "Automatic",
            "network_config_method_manual": "Manual",
            "network_config_address": "IPv4/CIDR",
            "network_config_gateway": "Gateway",
            "network_config_dns": "DNS",
            "network_config_check_ip": "Check IP",
            "network_config_apply": "Apply Config",
            "network_config_confirm": "Confirm Reachable",
            "network_config_rollback": "Rollback Now",
            "network_config_disabled": "Network config is disabled. Set NETWORK_CONFIG_ENABLED=true.",
            "network_config_backend_old": "The running backend does not include network config APIs. Restart or redeploy the updated backend.",
            "network_config_no_device": "No configurable connected interface",
            "network_config_ready": "Select an interface and fill in the config",
            "network_config_required": "Interface, IPv4/CIDR, gateway and DNS are required",
            "network_config_check_required": "Select an interface and enter an IPv4 address first",
            "network_config_ip_invalid": "Invalid IPv4 address. Use an address like 192.168.1.10/24.",
            "network_config_checking_ip": "Checking whether the IP is in use...",
            "network_config_ip_available": "No IP conflict detected, you can try using it",
            "network_config_ip_occupied": "This IP appears to be in use",
            "network_config_apply_confirm": "This will change the server network config. Confirm within 3 minutes or it rolls back automatically. Continue?",
            "network_config_applying": "Applying config...",
            "network_config_pending": "Config applied, waiting for confirmation, rollback at",
            "network_config_pending_active": "Network config pending confirmation, rollback countdown",
            "network_config_failed": "Network config failed",
            "network_config_no_pending": "No pending network config change",
            "network_config_confirmed": "Network reachability confirmed, rollback canceled",
            "network_config_rolled_back": "Network config rolled back",
            "host_bridge_title": "Bridge",
            "host_bridge_no_ethernet": "No ethernet NIC available (Wi-Fi cannot create a bridge)",
            "proxy_tun_title": "Proxy",
            "host_bridge_name": "Bridge name",
            "host_bridge_method": "Address mode",
            "host_bridge_method_inherit": "Inherit current NIC",
            "host_bridge_method_auto": "Automatic",
            "host_bridge_method_manual": "Manual",
            "host_bridge_create": "Create bridge",
            "host_bridge_confirm": "Confirm bridge OK",
            "host_bridge_rollback": "Rollback bridge",
            "host_bridge_dissolve": "Remove bridge",
            "host_bridge_create_confirm": "This will enslave the wired NIC into a bridge and move the host address onto the bridge. Confirm within 3 minutes or it rolls back. Continue?",
            "host_bridge_dissolve_confirm": "Removing the bridge restores the original NIC; devices on it will lose network. Continue?",
            "host_bridge_none": "No managed bridges",
            "host_bridge_for_device": "Managed bridge for NIC",
            "host_bridge_pending": "Bridge pending confirmation, rollback in",
            "host_bridge_creating": "Creating bridge...",
            "host_bridge_failed": "Bridge operation failed",
            "host_bridge_need_device": "Select a wired NIC first",
            "host_bridge_disabled": "Bridge management unavailable (needs Lazycat system API or local nmcli)",
            "host_bridge_select": "Select bridge",
            "host_bridge_select_need": "Select a bridge to remove first",
            "host_bridge_name_required": "Enter a bridge name suffix",
            "host_bridge_name_too_long": "Bridge name max 15 chars (including fixed prefix nw-)",
            "host_bridge_name_invalid": "Invalid bridge name: start with alnum, allow . _ - only",
            "host_bridge_name_prefix": "Bridge name must start with nw-",
            "host_bridge_preflight_skip": "Inherit/auto mode does not need an IP conflict check",
            "network_config_tab_ip": "Interface",
            "network_config_tab_bridge": "Bridge",
            "network_config_tab_dns": "DNS",
            "host_dns_method": "DNS mode",
            "host_dns_method_auto": "Automatic (DHCP)",
            "host_dns_method_manual": "Manual",
            "host_dns_servers": "DNS servers",
            "host_dns_apply": "Apply DNS",
            "host_dns_confirm": "Confirm OK",
            "host_dns_rollback": "Rollback now",
            "host_dns_pending": "DNS pending, rollback in",
            "host_dns_apply_confirm": "This changes host connection DNS via system SDK. Bad DNS can break the app; unconfirmed changes roll back in 60s. Continue?",
            "host_dns_applying": "Applying DNS…",
            "host_dns_failed": "DNS operation failed",
            "host_dns_disabled": "DNS management unavailable",
            "host_dns_target": "Target",
            "host_dns_runtime": "Runtime",
            "port_col": "Port",
            "owner_col": "Owner",
            "listen_addr_col": "Listen Address",
            "process_col": "Process",
            "container_col": "Container / App",
            "host_process": "Host Process",
            "host_owner": "Host",
            "host_services": "Host Services",
            "app_owner": "App",
            "unknown_process": "Unknown Process",
            "host_port_detail_title": "Port Details",
            "no_host_ports": "No listening ports",
            "port_search_label": "Search port",
            "port_search_placeholder": "Port number",
            "port_not_occupied": "No matching port in the current sample",
            "port_search_invalid": "Enter 1-5 digits",
            "min_rtt": "Min",
            "avg_rtt": "Avg",
            "max_rtt": "Max",
            "latency_jitter": "Jitter",
            "duration": "Duration",
            "total": "Total",
            "download_data": "Download",
            "upload_data": "Upload",
            "current_rate": "Latest Rate",
            "interval_avg": "Avg Rate",
            "interval_peak": "Peak Rate",
            "interval_total": "Total Volume",
            "latest": "Latest",
            "rx_total": "Upload Total",
            "tx_total": "Download Total",
            "app_traffic": "App Traffic",
            "samples": "Samples",
            "avg_interval": "Avg Interval",
            "first_sample": "First Sample",
            "last_sample": "Last Sample",
            "rx_delta": "Upload Delta",
            "tx_delta": "Download Delta",
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
            "lan_page_title": "NETWATCH // LAN Discovery",
            "website_latency_chk": "Website Latency",
            "domestic_sites": "Domestic Sites",
            "global_sites": "Global Sites",
            "identity_egress_title": "Network Identity & Egress IP",
            "proxy_env": "Proxy Environment",
            "default_gateway": "Default Gateway",
            "platform_connectivity": "Platform Connectivity",
            "nat_title": "NAT Type",
            "confidence": "Confidence",
            "confidence_high": "High",
            "confidence_medium": "Medium",
            "confidence_low": "Low",
            "checked_at": "Checked at",
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
            "broadband_steps_title": "Activity Log",
            "transfer_window_title": "Transfer Speed Test",
            "broadband_speedtest": "Broadband Speed Test",
            "domestic_nodes_only": "Domestic speedtest nodes only",
            "nic_realtime_monitor": "NIC Real-Time",
            "background_monitor": "Background Detection",
            "notification_settings": "Notification Settings",
            "notification_settings_desc": "Configure notification triggers, channels, device selection and do-not-disturb.",
            "notification_settings_btn": "Notification Settings",
            "notification_triggers": "Notification Triggers",
            "enable_notifications": "Enable notifications",
            "toggle_on": "On",
            "container_control": "Container Network Control",
            "no_containers": "No containers",
            "running": "Running",
            "blocked_internet": "No Internet",
            "blocked_all": "Blocked All",
            "block_internet_btn": "Block Net",
            "block_internet_title": "Block external access (LAN stays)",
            "unblock_btn": "Unblock",
            "container_blocked": "Container network blocked",
            "container_unblocked": "Container network restored",
            "operation_failed": "Operation failed",
            "client_notification": "Client notification",
            "notify_abnormal_traffic": "Abnormal traffic",
            "notify_egress_change": "Egress IP change",
            "notify_connectivity_change": "Connectivity down",
            "notify_lan_device_change": "LAN device changes",
            "lan_device_offline_after": "Offline after",
            "lan_device_online_after": "Online after",
            "lan_device_offline_notify_delay": "Offline delay",
            "lan_device_online_notify_delay": "Online delay",
            "bark_enabled": "Bark push",
            "bark_server_url": "Bark URL",
            "bark_device_key": "Bark Key",
            "bark_group": "Bark group",
            "test_bark_notification": "Test Bark push",
            "test_pushplus": "Test PushPlus",
            "pushplus_token": "PushPlus Token",
            "pushplus_topic": "PushPlus topic",
            "dnd_settings": "Do Not Disturb",
            "scheduled_notify": "Scheduled Notification",
            "notify_content": "Notification Content",
            "notify_content_edit": "Customize",
            "notify_content_desc": "Customize notification title and body templates. Leave empty for defaults.",
            "notify_template_title": "Title Template",
            "notify_template_body": "Body Template",
            "notify_template_vars": "Available variables:",
            "lan_max_check_attempts": "Confirm count",
            "lan_notify_cooldown_sec": "Notification cooldown",
            "lan_flapping_threshold": "Flapping suppression threshold",
            "lan_flapping_window_sec": "Flapping detection window",
            "lan_device_auto_remove_days": "Auto-remove",
            "device_col": "Device",
            "network_info_col": "Network Info",
            "last_seen_col": "Last Seen",
            "action_col": "Action",
            "scan_btn": "Scan",
            "notification_devices": "Notification Devices",
            "no_devices_registered": "No registered devices",
            "status_online": "Online",
            "status_offline": "Offline",
            "status_iface_down": "Iface down",
            "status_pending": "Pending",
            "status_online_confirming": "Confirming",
            "status_unknown": "Unknown",
            "test_notify_sent": "Test notification sent",
            "test_notify_failed": "Test notification failed",
            "settings_saved": "Settings saved",
            "settings_save_failed": "Settings save failed",
            "lan_load_failed": "LAN device load failed",
            "device_mark_updated": "Device mark updated",
            "device_mark_failed": "Device mark update failed",
            "test_notify_title": "Netwatch Test",
            "test_notify_body": "Client notification API is working.",
            "traffic_analysis_settings": "Traffic Analysis Settings",
            "enable_traffic_analysis": "Enable Traffic Analysis",
            "per_app_sampling": "Per-App Sampling",
            "traffic_page_subtitle": "App Traffic Analysis",
            "back_to_dashboard": "Back to Dashboard",
            "app_list_title": "Apps",
            "search_app_placeholder": "Search apps",
            "sort_total_desc": "Total traffic desc",
            "sort_rx_desc": "Upload desc",
            "sort_tx_desc": "Download desc",
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
            "trace_stop": "Stop",
            "trace_stopped": "Stopped",
            "trace_done": "Done",
            "trace_running_hint": "Trace running — open hops or stop",
            "trace_initial_hint": "Enter a target to start path diagnostics",
            "trace_details_btn": "View Hops",
            "trace_window_note": "Shows the full hop list and latency details.",
            "trace_not_started": "Trace not started",
            "waiting_for_sample": "Waiting for samples",
            "waiting_data": "Waiting for data",
            "traffic_analysis_btn": "Traffic Analysis",
            "traffic_live_rate": "Live rate",
            "traffic_open_settings_hint": "Enable traffic analysis in settings first",
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
            "sampling_off_hint": "sampling off",
            "not_detected": "Not detected",
            "ipv6_checking": "Checking",
            "ipv6_fully_usable": "IPv6 fully usable",
            "ipv6_outbound_only": "IPv6 outbound only",
            "ipv6_address_only": "Address only · no route",
            "ipv6_no_global": "No global IPv6",
            "ipv6_layer_addr": "Global address",
            "ipv6_layer_outbound": "Outbound",
            "ipv6_layer_https": "HTTPS over IPv6",
            "ipv6_layer_dns": "DNS AAAA",
            "ipv6_view_detail": "Details",
            "ipv6_detail_conclusion": "Overall",
            "ipv6_detail_title": "IPv6 Availability Details",
            "ipv6_detail_note": "Layered check of real IPv6 usability: address → outbound → application → DNS, all via domestic targets.",
            "ipv6_detail_addr_desc": "Whether the host has a globally routable public IPv6 address (excludes link-local fe80::/ULA fc00::).",
            "ipv6_detail_outbound_desc": "tcp6 reachability to domestic anycast (Alibaba DNS); confirms IPv6 routing works.",
            "ipv6_detail_https_desc": "HTTPS over IPv6 to a domestic dual-stack site (Baidu/Taobao); confirms the application layer truly works.",
            "ipv6_detail_dns_desc": "Whether a domestic domain resolves an AAAA record; prerequisite for apps using IPv6.",
            "ipv6_detail_target": "Outbound target",
            "ipv6_detail_checked_at": "Checked at",
            "ipv6_renew_title": "IPv6 Address Renewal",
            "ipv6_renew_note": "Re-applies the NIC config via the system NetworkManager (nmcli device reapply) to re-acquire IPv6. Use when the lease expired but the address didn't refresh.",
            "ipv6_renew_select_nic": "Select NIC",
            "ipv6_renew_exec": "Re-acquire IPv6",
            "ipv6_renew_refresh": "Refresh NICs",
            "ipv6_renew_no_nic": "No renewable NIC found (NetworkManager unavailable or non-Lazycat env)",
            "ipv6_renew_unavailable": "Not supported (needs Lazycat system API or local nmcli)",
            "ipv6_renew_running": "Renewing...",
            "ipv6_renew_ok": "Renewed. IPv6 re-acquired.",
            "ipv6_renew_failed": "Renewal failed",
            "no_monitored_nics": "No monitored NICs",
            "rx": "Download",
            "tx": "Upload",
            "app_traffic_upload": "Upload",
            "app_traffic_download": "Download",
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
            "sec_300": "300 sec",
            "confirm_cancel_test": "A speed test is in progress. Close anyway?",
            "confirm_cancel_title": "Cancel Speed Test",
            "confirm_cancel_ok": "Cancel Test",
            "confirm_cancel_keep": "Keep Testing"
        }
    };

    function getLang() {
        var langs = navigator.languages && navigator.languages.length ? navigator.languages : [navigator.language || 'zh-CN'];
        for (var i = 0; i < langs.length; i++) {
            var lang = String(langs[i] || '').toLowerCase();
            if (lang.indexOf('zh') === 0) return 'zh-CN';
            if (lang.indexOf('en') === 0) return 'en';
        }
        return 'en';
    }

    function setLang(lang) {
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
            // Never open menus for selects sitting in a closed floating window
            // (menus are portaled to body and would float over the homepage).
            var hostWin = select.closest('.floating-window');
            if (hostWin && !hostWin.classList.contains('active')) return;
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
        // Do NOT set aria-hidden on the native select: browsers may still focus it
        // (label click / autofocus / form reset) and then warn about aria-hidden focus.
        select.removeAttribute('aria-hidden');
        select.tabIndex = -1;
        select.setAttribute('aria-label', select.getAttribute('aria-label') || select.name || 'select');
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
        root.querySelectorAll('[data-i18n-aria-label]').forEach(function (el) {
            var key = el.getAttribute('data-i18n-aria-label');
            if (key) {
                el.setAttribute('aria-label', window.__(key));
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
    };

    window.applyI18n = applyI18n;
    window.enhanceSelects = enhanceSelects;
    window.syncCustomSelect = syncCustomSelect;
    window.closeCustomSelects = closeCustomSelects;

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
