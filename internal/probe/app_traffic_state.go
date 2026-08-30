package probe

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"netwatch/internal/logger"
)

const (
	appTrafficSampleInterval  = 2 * time.Second
	appTrafficPersistInterval = 30 * time.Second
	appTrafficHistoryInterval = time.Minute
	maxAppTrafficDailyRecords = 93
	// Keep one point per minute for a full day, matching the useful history
	// horizon available before traffic was grouped by application.
	maxAppTrafficSamples = 24 * 60
)

// AppTrafficLimit is a per-application bandwidth ceiling. A zero value means
// unlimited for that direction. Rates are expressed in Kbit/s because that is
// tc's stable, integer-friendly unit.
type AppTrafficLimit struct {
	UploadKbps   int64  `json:"upload_kbps,omitempty"`
	DownloadKbps int64  `json:"download_kbps,omitempty"`
	UpdatedAt    string `json:"updated_at,omitempty"`
}

type AppTrafficDailyRecord struct {
	Date          string `json:"date"`
	UploadBytes   uint64 `json:"upload_bytes"`
	DownloadBytes uint64 `json:"download_bytes"`
}

type AppTrafficSample struct {
	Timestamp     string `json:"timestamp"`
	UploadTotal   uint64 `json:"upload_total"`
	DownloadTotal uint64 `json:"download_total"`
}

// legacyAppTrafficPoint is the on-disk format used before application traffic
// was keyed by app_id. It is deliberately kept private: it is read only for a
// one-time migration from bridge-level history.
type legacyAppTrafficPoint struct {
	Timestamp     string `json:"timestamp"`
	RxBytes       uint64 `json:"rx_bytes"`
	TxBytes       uint64 `json:"tx_bytes"`
	UploadBytes   uint64 `json:"upload_bytes"`
	DownloadBytes uint64 `json:"download_bytes"`
	Discontinuity bool   `json:"discontinuity,omitempty"`
}

// AppTrafficUsage is an application-level view. TotalUpload and TotalDownload
// are persistent lifetime totals for the application. The first observation
// seeds them from the current bridge counters, while later samples add only
// positive counter deltas. This keeps the lifetime total monotonic when a
// bridge is recreated or its kernel counters reset.
type AppTrafficUsage struct {
	AppID                  string                  `json:"app_id"`
	AppTitle               string                  `json:"app_title,omitempty"`
	Project                string                  `json:"project,omitempty"`
	Icon                   string                  `json:"icon,omitempty"`
	StatusText             string                  `json:"status_text,omitempty"`
	CreatedAt              int64                   `json:"created_at,omitempty"`
	Bridges                []string                `json:"bridges,omitempty"`
	ContainerCount         int                     `json:"container_count,omitempty"`
	RunningCount           int                     `json:"running_count,omitempty"`
	UploadBPS              float64                 `json:"upload_bps"`
	DownloadBPS            float64                 `json:"download_bps"`
	TodayUpload            uint64                  `json:"today_upload"`
	TodayDownload          uint64                  `json:"today_download"`
	MonthUpload            uint64                  `json:"month_upload"`
	MonthDownload          uint64                  `json:"month_download"`
	TotalUpload            uint64                  `json:"total_upload"`
	TotalDownload          uint64                  `json:"total_download"`
	Limit                  AppTrafficLimit         `json:"limit"`
	SampledAt              string                  `json:"sampled_at,omitempty"`
	FirstSampledAt         string                  `json:"first_sampled_at,omitempty"`
	Daily                  []AppTrafficDailyRecord `json:"daily,omitempty"`
	NetworkModes           []string                `json:"network_modes,omitempty"`
	NetworkTargets         []AppNetworkTarget      `json:"network_targets,omitempty"`
	NetworkTopology        string                  `json:"network_topology,omitempty"`
	TrafficLimitAllowed    bool                    `json:"traffic_limit_allowed"`
	InternetControlAllowed bool                    `json:"internet_control_allowed"`
	NetworkPolicy          AppNetworkPolicyStatus  `json:"network_policy"`
}

type AppTrafficOverview struct {
	GeneratedAt  string            `json:"generated_at"`
	Apps         []AppTrafficUsage `json:"apps"`
	LimitSupport bool              `json:"limit_support"`
	Note         string            `json:"note,omitempty"`
}

type appTrafficBridgeBaseline struct {
	AppID         string `json:"app_id"`
	UploadBytes   uint64 `json:"upload_bytes"`
	DownloadBytes uint64 `json:"download_bytes"`
}

// appTrafficBaselineKey keeps independent kernel counters independent. Bridge
// counters are naturally keyed by interface; Host counters must be keyed by
// cgroup path because one application can own several Host containers.
func appTrafficBaselineKey(item AppBridgeStats) string {
	if item.NetworkMode == "host" || strings.HasPrefix(strings.TrimSpace(item.Bridge), hostAppTargetPrefix) {
		if cgroup := strings.TrimSpace(item.CgroupPath); cgroup != "" {
			return "host-cgroup:" + cgroup
		}
		return "host-target:" + strings.TrimSpace(item.Bridge)
	}
	// Preserve the legacy on-disk key for Bridge counters so upgrading does
	// not seed a second baseline and double-count the first post-upgrade delta.
	return strings.TrimSpace(item.Bridge)
}

type appTrafficStoredUsage struct {
	AppTrafficUsage
	Samples []AppTrafficSample `json:"samples,omitempty"`
}

type appTrafficPersistedState struct {
	Apps map[string]appTrafficStoredUsage `json:"apps"`
	// Baselines is keyed by bridge for Bridge counters and by stable cgroup
	// path for Host counters. Keep the JSON field name for on-disk compatibility.
	Baselines     map[string]appTrafficBridgeBaseline `json:"baselines"`
	Limits        map[string]AppTrafficLimit          `json:"limits"`
	LegacyBridges map[string][]legacyAppTrafficPoint  `json:"legacy_bridges,omitempty"`
}

type appTrafficState struct {
	mu            sync.RWMutex
	path          string
	apps          map[string]appTrafficStoredUsage
	baselines     map[string]appTrafficBridgeBaseline
	limits        map[string]AppTrafficLimit
	legacyBridges map[string][]legacyAppTrafficPoint
	lastSample    time.Time
	lastPersist   time.Time
}

func newAppTrafficState(dataDir string) *appTrafficState {
	s := &appTrafficState{
		path:          filepath.Join(dataDir, "app_traffic_history.json"),
		apps:          map[string]appTrafficStoredUsage{},
		baselines:     map[string]appTrafficBridgeBaseline{},
		limits:        map[string]AppTrafficLimit{},
		legacyBridges: map[string][]legacyAppTrafficPoint{},
	}
	body, err := os.ReadFile(s.path)
	if err != nil {
		return s
	}
	var persisted appTrafficPersistedState
	if err := jsonUnmarshal(body, &persisted); err != nil {
		logger.Warn("load app traffic history: %v", err)
		return s
	}
	if persisted.Apps != nil || persisted.Baselines != nil || persisted.Limits != nil || persisted.LegacyBridges != nil {
		if persisted.Apps != nil {
			s.apps = persisted.Apps
			normalizePersistedAppTrafficTotals(s.apps)
		}
		if persisted.Baselines != nil {
			s.baselines = persisted.Baselines
		}
		if persisted.Limits != nil {
			s.limits = persisted.Limits
		}
		if persisted.LegacyBridges != nil {
			s.legacyBridges = persisted.LegacyBridges
		}
		return s
	}

	// Versions before c148163 stored a top-level map of bridge name to raw
	// counter samples. Preserve it until a live bridge can be resolved to an
	// app_id; writing an empty v2 state here would otherwise destroy history.
	legacy := map[string][]legacyAppTrafficPoint{}
	if err := jsonUnmarshal(body, &legacy); err != nil || len(legacy) == 0 {
		if err != nil {
			logger.Warn("load legacy app traffic history: %v", err)
		}
		return s
	}
	for bridge, points := range legacy {
		if strings.HasPrefix(bridge, lzcBridgePrefix) && len(points) > 0 {
			s.legacyBridges[bridge] = points
		}
	}
	if len(s.legacyBridges) > 0 {
		logger.Info("app traffic: %d legacy bridge histories pending app_id migration", len(s.legacyBridges))
	}
	return s
}

// jsonUnmarshal is assigned in tests only when a malformed state needs to be
// injected without touching the filesystem API.
var jsonUnmarshal = func(data []byte, target any) error {
	return json.Unmarshal(data, target)
}

type appTrafficDelta struct {
	upload   uint64
	download uint64
}

type appTrafficAbsolute struct {
	upload   uint64
	download uint64
}

// migrateLegacyBridgesLocked converts the former bridge-keyed counter history
// once the current bridge can be authoritatively mapped to an app_id. Old
// samples hold absolute kernel counters, so only positive point-to-point deltas
// are migrated; counter resets and declared discontinuities stay out of totals.
func (s *appTrafficState) migrateLegacyBridgesLocked(items []AppBridgeStats, now time.Time) {
	if len(s.legacyBridges) == 0 {
		return
	}
	bridgeApps := make(map[string]string, len(items))
	for _, item := range items {
		bridge := strings.TrimSpace(item.Bridge)
		appID := strings.TrimSpace(item.AppID)
		if bridge == "" || appID == "" || isNetwatchTrafficItem(item) {
			continue
		}
		if _, exists := s.legacyBridges[bridge]; exists {
			bridgeApps[bridge] = appID
		}
	}
	if len(bridgeApps) == 0 {
		return
	}

	perAppDeltas := make(map[string]map[string]appTrafficDelta)
	perAppSamples := make(map[string]map[string]appTrafficAbsolute)
	migrated := make([]string, 0, len(bridgeApps))
	for bridge, appID := range bridgeApps {
		byTimestamp := perAppDeltas[appID]
		if byTimestamp == nil {
			byTimestamp = make(map[string]appTrafficDelta)
			perAppDeltas[appID] = byTimestamp
		}
		for timestamp, delta := range legacyAppTrafficDeltas(s.legacyBridges[bridge]) {
			combined := byTimestamp[timestamp]
			combined.upload += delta.upload
			combined.download += delta.download
			byTimestamp[timestamp] = combined
		}
		rawSamples := perAppSamples[appID]
		if rawSamples == nil {
			rawSamples = make(map[string]appTrafficAbsolute)
			perAppSamples[appID] = rawSamples
		}
		for timestamp, total := range legacyAppTrafficRawTotals(s.legacyBridges[bridge]) {
			combined := rawSamples[timestamp]
			combined.upload += total.upload
			combined.download += total.download
			rawSamples[timestamp] = combined
		}
		migrated = append(migrated, bridge)
	}

	for appID, byTimestamp := range perAppDeltas {
		entry := s.apps[appID]
		entry.AppID = appID
		for _, timestamp := range sortedTrafficTimestamps(byTimestamp) {
			delta := byTimestamp[timestamp]
			if sampledAt, err := time.ParseInLocation(time.DateTime, timestamp, time.Local); err == nil {
				day := sampledAt.Format(time.DateOnly)
				entry.Daily, _, _ = updateTrafficPeriod(entry.Daily, day, delta.upload, delta.download)
			}
			entry.FirstSampledAt = earlierTrafficTimestamp(entry.FirstSampledAt, timestamp)
		}
		rawSamples := perAppSamples[appID]
		legacySamples := make([]AppTrafficSample, 0, len(rawSamples))
		for _, timestamp := range sortedTrafficAbsolutes(rawSamples) {
			total := rawSamples[timestamp]
			legacySamples = append(legacySamples, AppTrafficSample{
				Timestamp: timestamp, UploadTotal: total.upload, DownloadTotal: total.download,
			})
		}
		entry.Samples = mergeAppTrafficSamples(entry.Samples, legacySamples)
		entry.Daily = trimTrafficDaily(entry.Daily)
		entry.TodayUpload, entry.TodayDownload = trafficDailyTotals(entry.Daily, now.Format(time.DateOnly))
		entry.MonthUpload, entry.MonthDownload = trafficMonthTotals(entry.Daily, now.Format("2006-01"))
		for _, sample := range legacySamples {
			if entry.TotalUpload < sample.UploadTotal {
				entry.TotalUpload = sample.UploadTotal
			}
			if entry.TotalDownload < sample.DownloadTotal {
				entry.TotalDownload = sample.DownloadTotal
			}
		}
		if upload := trafficDailySum(entry.Daily, "upload"); entry.TotalUpload < upload {
			entry.TotalUpload = upload
		}
		if download := trafficDailySum(entry.Daily, "download"); entry.TotalDownload < download {
			entry.TotalDownload = download
		}
		entry.Samples = trimAppTrafficSamples(entry.Samples)
		s.apps[appID] = entry
	}
	for _, bridge := range migrated {
		delete(s.legacyBridges, bridge)
	}
	logger.Info("app traffic: migrated %d legacy bridge histories", len(migrated))
}

func legacyAppTrafficRawTotals(points []legacyAppTrafficPoint) map[string]appTrafficAbsolute {
	out := make(map[string]appTrafficAbsolute, len(points))
	for _, point := range points {
		if point.Timestamp == "" {
			continue
		}
		out[point.Timestamp] = appTrafficAbsolute{
			upload: legacyUploadBytes(point), download: legacyDownloadBytes(point),
		}
	}
	return out
}

func mergeAppTrafficSamples(existing, imported []AppTrafficSample) []AppTrafficSample {
	byTimestamp := make(map[string]AppTrafficSample, len(existing)+len(imported))
	for _, sample := range imported {
		if sample.Timestamp != "" {
			byTimestamp[sample.Timestamp] = sample
		}
	}
	// Native app-level samples take precedence at duplicate timestamps because
	// they can include more than one bridge for an app.
	for _, sample := range existing {
		if sample.Timestamp != "" {
			byTimestamp[sample.Timestamp] = sample
		}
	}
	out := make([]AppTrafficSample, 0, len(byTimestamp))
	for _, sample := range byTimestamp {
		out = append(out, sample)
	}
	sort.SliceStable(out, func(i, j int) bool {
		return trafficTimestampBefore(out[i].Timestamp, out[j].Timestamp)
	})
	return out
}

func earlierTrafficTimestamp(current, candidate string) string {
	if current == "" || trafficTimestampBefore(candidate, current) {
		return candidate
	}
	return current
}

func trafficTimestampBefore(left, right string) bool {
	leftTime, leftErr := time.ParseInLocation(time.DateTime, left, time.Local)
	rightTime, rightErr := time.ParseInLocation(time.DateTime, right, time.Local)
	if leftErr == nil && rightErr == nil {
		return leftTime.Before(rightTime)
	}
	return left < right
}

func legacyAppTrafficDeltas(points []legacyAppTrafficPoint) map[string]appTrafficDelta {
	ordered := append([]legacyAppTrafficPoint(nil), points...)
	sort.SliceStable(ordered, func(i, j int) bool {
		left, leftErr := time.ParseInLocation(time.DateTime, ordered[i].Timestamp, time.Local)
		right, rightErr := time.ParseInLocation(time.DateTime, ordered[j].Timestamp, time.Local)
		if leftErr != nil || rightErr != nil {
			return ordered[i].Timestamp < ordered[j].Timestamp
		}
		return left.Before(right)
	})
	deltas := make(map[string]appTrafficDelta, len(ordered))
	for index, point := range ordered {
		if point.Timestamp == "" {
			continue
		}
		delta := deltas[point.Timestamp]
		if index > 0 {
			previous := ordered[index-1]
			upload, previousUpload := legacyUploadBytes(point), legacyUploadBytes(previous)
			download, previousDownload := legacyDownloadBytes(point), legacyDownloadBytes(previous)
			if !point.Discontinuity && upload >= previousUpload && download >= previousDownload {
				delta.upload += upload - previousUpload
				delta.download += download - previousDownload
			}
		}
		deltas[point.Timestamp] = delta
	}
	return deltas
}

func legacyUploadBytes(point legacyAppTrafficPoint) uint64 {
	if point.UploadBytes != 0 || point.RxBytes == 0 {
		return point.UploadBytes
	}
	return point.RxBytes
}

func legacyDownloadBytes(point legacyAppTrafficPoint) uint64 {
	if point.DownloadBytes != 0 || point.TxBytes == 0 {
		return point.DownloadBytes
	}
	return point.TxBytes
}

func sortedTrafficTimestamps(values map[string]appTrafficDelta) []string {
	timestamps := make([]string, 0, len(values))
	for timestamp := range values {
		timestamps = append(timestamps, timestamp)
	}
	sort.Slice(timestamps, func(i, j int) bool { return trafficTimestampBefore(timestamps[i], timestamps[j]) })
	return timestamps
}

func sortedTrafficAbsolutes(values map[string]appTrafficAbsolute) []string {
	timestamps := make([]string, 0, len(values))
	for timestamp := range values {
		timestamps = append(timestamps, timestamp)
	}
	sort.Slice(timestamps, func(i, j int) bool { return trafficTimestampBefore(timestamps[i], timestamps[j]) })
	return timestamps
}

func (s *appTrafficState) sample(items []AppBridgeStats, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.lastSample.IsZero() && now.Sub(s.lastSample) < appTrafficSampleInterval/2 {
		return
	}
	elapsed := now.Sub(s.lastSample).Seconds()
	if elapsed <= 0 {
		elapsed = 0
	}
	s.migrateLegacyBridgesLocked(items, now)

	seen := make(map[string]bool, len(items))
	changes := make(map[string]appTrafficDelta)
	observed := make(map[string]AppTrafficUsage)
	for _, item := range items {
		bridge := strings.TrimSpace(item.Bridge)
		if bridge == "" {
			continue
		}
		// Metadata enrichment can briefly fail while the underlying bridge or
		// cgroup counter remains alive. Keep its baseline in that case, otherwise
		// the next successful lookup would silently discard bytes from the gap.
		baselineKey := appTrafficBaselineKey(item)
		seen[baselineKey] = true
		appID := strings.TrimSpace(item.AppID)
		if appID == "" || isNetwatchTrafficItem(item) {
			continue
		}
		baseline, known := s.baselines[baselineKey]
		if !known || baseline.AppID != appID || item.UploadBytes < baseline.UploadBytes || item.DownloadBytes < baseline.DownloadBytes {
			// A new bridge or a reset counter establishes a baseline. Counting its
			// pre-existing bytes would attribute traffic from before observation.
			s.baselines[baselineKey] = appTrafficBridgeBaseline{AppID: appID, UploadBytes: item.UploadBytes, DownloadBytes: item.DownloadBytes}
		} else {
			delta := changes[appID]
			delta.upload += item.UploadBytes - baseline.UploadBytes
			delta.download += item.DownloadBytes - baseline.DownloadBytes
			changes[appID] = delta
			s.baselines[baselineKey] = appTrafficBridgeBaseline{AppID: appID, UploadBytes: item.UploadBytes, DownloadBytes: item.DownloadBytes}
		}

		entry := observed[appID]
		entry.AppID = appID
		entry.AppTitle = preferAppTrafficValue(item.AppTitle, entry.AppTitle)
		entry.Project = preferAppTrafficValue(item.Project, entry.Project)
		entry.Icon = preferAppTrafficValue(item.Icon, entry.Icon)
		mergeAppTrafficStart(&entry, item.StatusText, item.CreatedAt)
		entry.ContainerCount += item.ContainerCount
		entry.RunningCount += item.RunningCount
		entry.TotalUpload += item.UploadBytes
		entry.TotalDownload += item.DownloadBytes
		entry.Bridges = append(entry.Bridges, bridge)
		if target, ok := appNetworkTargetFromStats(item); ok {
			entry.NetworkTargets = appendUniqueAppNetworkTarget(entry.NetworkTargets, target)
		}
		if item.NetworkMode != "" {
			entry.NetworkModes = appendUniqueTrafficValue(entry.NetworkModes, item.NetworkMode)
		}
		observed[appID] = entry
	}
	for counterKey := range s.baselines {
		if !seen[counterKey] {
			delete(s.baselines, counterKey)
		}
	}

	today := now.Format(time.DateOnly)
	month := now.Format("2006-01")
	for appID, current := range observed {
		entry := s.apps[appID]
		hadHistory := entry.AppID != ""
		entry.AppID = appID
		entry.AppTitle = preferAppTrafficValue(current.AppTitle, entry.AppTitle)
		entry.Project = preferAppTrafficValue(current.Project, entry.Project)
		entry.Icon = preferAppTrafficValue(current.Icon, entry.Icon)
		mergeAppTrafficStart(&entry.AppTrafficUsage, current.StatusText, current.CreatedAt)
		entry.ContainerCount = current.ContainerCount
		entry.RunningCount = current.RunningCount
		entry.NetworkModes = append([]string(nil), current.NetworkModes...)
		entry.NetworkTargets = append([]AppNetworkTarget(nil), current.NetworkTargets...)
		if !hadHistory {
			entry.TotalUpload = current.TotalUpload
			entry.TotalDownload = current.TotalDownload
		}
		change := changes[appID]
		entry.TotalUpload += change.upload
		entry.TotalDownload += change.download
		if dailyUpload := trafficDailySum(entry.Daily, "upload"); entry.TotalUpload < dailyUpload {
			entry.TotalUpload = dailyUpload
		}
		if dailyDownload := trafficDailySum(entry.Daily, "download"); entry.TotalDownload < dailyDownload {
			entry.TotalDownload = dailyDownload
		}
		entry.Bridges = dedupeSortedStrings(current.Bridges)
		if entry.FirstSampledAt == "" {
			entry.FirstSampledAt = now.Format(time.DateTime)
		}
		entry.SampledAt = now.Format(time.DateTime)
		s.apps[appID] = entry
	}
	for appID, entry := range s.apps {
		if _, isObserved := observed[appID]; !isObserved {
			entry.UploadBPS = 0
			entry.DownloadBPS = 0
			s.apps[appID] = entry
			continue
		}
		change := changes[appID]
		if elapsed > 0 {
			entry.UploadBPS = float64(change.upload) / elapsed
			entry.DownloadBPS = float64(change.download) / elapsed
		} else {
			entry.UploadBPS = 0
			entry.DownloadBPS = 0
		}
		entry.Daily, entry.TodayUpload, entry.TodayDownload = updateTrafficPeriod(entry.Daily, today, change.upload, change.download)
		entry.MonthUpload, entry.MonthDownload = trafficMonthTotals(entry.Daily, month)
		entry.Daily = trimTrafficDaily(entry.Daily)
		if last := len(entry.Samples) - 1; last < 0 || sampleDue(entry.Samples[last].Timestamp, now) {
			entry.Samples = append(entry.Samples, AppTrafficSample{Timestamp: now.Format(time.DateTime), UploadTotal: entry.TotalUpload, DownloadTotal: entry.TotalDownload})
			entry.Samples = trimAppTrafficSamples(entry.Samples)
		}
		s.apps[appID] = entry
	}
	s.lastSample = now
	if now.Sub(s.lastPersist) >= appTrafficPersistInterval {
		s.persistLocked(now)
	}
}

// normalizePersistedAppTrafficTotals upgrades states written while TotalUpload
// and TotalDownload represented only the current native bridge counters. The
// retained daily history is a lower bound for the lifetime totals, so this
// conversion immediately removes the impossible "today > total" display
// without inventing traffic outside the data already on disk.
func normalizePersistedAppTrafficTotals(apps map[string]appTrafficStoredUsage) {
	for appID, entry := range apps {
		entry.AppID = preferAppTrafficValue(entry.AppID, appID)
		if upload := trafficDailySum(entry.Daily, "upload"); entry.TotalUpload < upload {
			entry.TotalUpload = upload
		}
		if download := trafficDailySum(entry.Daily, "download"); entry.TotalDownload < download {
			entry.TotalDownload = download
		}
		apps[appID] = entry
	}
}

func trafficDailySum(records []AppTrafficDailyRecord, direction string) uint64 {
	var total uint64
	for _, record := range records {
		if direction == "upload" {
			total += record.UploadBytes
		} else {
			total += record.DownloadBytes
		}
	}
	return total
}

func isNetwatchTrafficItem(item AppBridgeStats) bool {
	return strings.EqualFold(strings.TrimSpace(item.AppID), "cloud.lazycat.app.netwatch") ||
		strings.EqualFold(strings.TrimSpace(item.AppID), "netwatch")
}

func preferAppTrafficValue(current, fallback string) string {
	if strings.TrimSpace(current) != "" {
		return current
	}
	return fallback
}

func mergeAppTrafficStart(entry *AppTrafficUsage, statusText string, createdAt int64) {
	// The runtime metadata is sourced from the application's primary `app`
	// container. Replace stale persisted/sidecar timestamps when a current
	// primary timestamp is available, including after an app restart.
	if createdAt > 0 {
		entry.CreatedAt = createdAt
		if statusText != "" {
			entry.StatusText = statusText
		}
		return
	}
	if entry.StatusText == "" && statusText != "" {
		entry.StatusText = statusText
	}
}

func updateTrafficPeriod(records []AppTrafficDailyRecord, day string, upload, download uint64) ([]AppTrafficDailyRecord, uint64, uint64) {
	for i := range records {
		if records[i].Date == day {
			records[i].UploadBytes += upload
			records[i].DownloadBytes += download
			return records, records[i].UploadBytes, records[i].DownloadBytes
		}
	}
	records = append(records, AppTrafficDailyRecord{Date: day, UploadBytes: upload, DownloadBytes: download})
	return records, upload, download
}

func trafficMonthTotals(records []AppTrafficDailyRecord, month string) (uint64, uint64) {
	var upload, download uint64
	for _, record := range records {
		if strings.HasPrefix(record.Date, month+"-") {
			upload += record.UploadBytes
			download += record.DownloadBytes
		}
	}
	return upload, download
}

func trafficDailyTotals(records []AppTrafficDailyRecord, day string) (uint64, uint64) {
	for _, record := range records {
		if record.Date == day {
			return record.UploadBytes, record.DownloadBytes
		}
	}
	return 0, 0
}

func trimTrafficDaily(records []AppTrafficDailyRecord) []AppTrafficDailyRecord {
	sort.Slice(records, func(i, j int) bool { return records[i].Date < records[j].Date })
	if len(records) > maxAppTrafficDailyRecords {
		records = records[len(records)-maxAppTrafficDailyRecords:]
	}
	return records
}

func sampleDue(timestamp string, now time.Time) bool {
	previous, err := time.ParseInLocation(time.DateTime, timestamp, time.Local)
	return err != nil || now.Sub(previous) >= appTrafficHistoryInterval
}

func trimAppTrafficSamples(samples []AppTrafficSample) []AppTrafficSample {
	if len(samples) > maxAppTrafficSamples {
		return samples[len(samples)-maxAppTrafficSamples:]
	}
	return samples
}

func dedupeSortedStrings(in []string) []string {
	set := make(map[string]bool, len(in))
	for _, value := range in {
		if value != "" {
			set[value] = true
		}
	}
	out := make([]string, 0, len(set))
	for value := range set {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func (s *appTrafficState) overview(limitSupported bool) AppTrafficOverview {
	return s.overviewForActiveApps(limitSupported, nil)
}

// overviewForActiveApps returns the persisted application history while
// optionally limiting the visible list to applications that are present in
// the current runtime snapshot. History stays on disk so a stopped app can
// resume with its previous totals when it is started again, but inactive
// applications do not remain as zero-rate rows in the live dashboard.
func (s *appTrafficState) overviewForActiveApps(limitSupported bool, activeAppIDs map[string]bool) AppTrafficOverview {
	return s.overviewForActiveAppsWithControls(limitSupported, activeAppIDs, true)
}

func (s *appTrafficState) overviewForActiveAppsWithControls(limitSupported bool, activeAppIDs map[string]bool, hostControlsEnabled bool) AppTrafficOverview {
	s.mu.RLock()
	defer s.mu.RUnlock()
	apps := make([]AppTrafficUsage, 0, len(s.apps))
	for appID, entry := range s.apps {
		if entry.AppID == "" {
			entry.AppID = appID
		}
		if activeAppIDs != nil && !activeAppIDs[entry.AppID] {
			continue
		}
		entry.Limit = s.limits[appID]
		entry.Samples = nil
		entry.Daily = append([]AppTrafficDailyRecord(nil), entry.Daily...)
		entry.Bridges = append([]string(nil), entry.Bridges...)
		entry.NetworkModes = append([]string(nil), entry.NetworkModes...)
		entry.NetworkTargets = append([]AppNetworkTarget(nil), entry.NetworkTargets...)
		entry.NetworkTopology, entry.TrafficLimitAllowed, entry.InternetControlAllowed = appTrafficControlCapabilitiesWithControls(
			entry.AppTrafficUsage, limitSupported, hostControlsEnabled,
		)
		apps = append(apps, entry.AppTrafficUsage)
	}
	sort.Slice(apps, func(i, j int) bool {
		return apps[i].TotalUpload+apps[i].TotalDownload > apps[j].TotalUpload+apps[j].TotalDownload
	})
	return AppTrafficOverview{GeneratedAt: time.Now().Format(time.DateTime), Apps: apps, LimitSupport: limitSupported}
}

func appTrafficControlCapabilities(entry AppTrafficUsage, limitSupported bool) (topology string, limitAllowed bool, internetAllowed bool) {
	return appTrafficControlCapabilitiesWithControls(entry, limitSupported, true)
}

func appTrafficControlCapabilitiesWithControls(entry AppTrafficUsage, limitSupported bool, hostControlsEnabled bool) (topology string, limitAllowed bool, internetAllowed bool) {
	hasBridge := false
	hasHost := false
	for _, target := range entry.NetworkTargets {
		switch target.Kind {
		case AppNetworkTargetBridge:
			hasBridge = true
		case AppNetworkTargetCgroup:
			hasHost = true
		}
	}
	// Records written before AppNetworkTarget was introduced only have the
	// legacy bridge strings. Use them as a read-time compatibility fallback.
	if len(entry.NetworkTargets) == 0 {
		for _, mode := range entry.NetworkModes {
			switch mode {
			case "bridge":
				hasBridge = true
			case "host":
				hasHost = true
			}
		}
		for _, target := range entry.Bridges {
			if strings.HasPrefix(target, lzcBridgePrefix) {
				hasBridge = true
			}
			if strings.HasPrefix(target, hostAppTargetPrefix) {
				hasHost = true
			}
		}
	}
	switch {
	case hasBridge && hasHost:
		topology = "mixed"
	case hasHost:
		topology = "host"
	case hasBridge:
		topology = "bridge"
	default:
		topology = "unknown"
	}
	if isWhitelistedApp(entry.AppID, entry.AppTitle) {
		return topology, false, false
	}
	if hasHost && !hostControlsEnabled {
		return topology, false, false
	}
	// Pure Bridge uses per-bridge TBF/police. Host and Mixed applications use
	// the shared physical-device TC classifier when experimental controls are on.
	limitAllowed = limitSupported && (hasBridge || hasHost)
	internetAllowed = hasBridge || hasHost
	return topology, limitAllowed, internetAllowed
}

func appendUniqueTrafficValue(values []string, candidate string) []string {
	for _, value := range values {
		if value == candidate {
			return values
		}
	}
	return append(values, candidate)
}

func (s *appTrafficState) history(appID string) []AppTrafficSample {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]AppTrafficSample{}, s.apps[appID].Samples...)
}

func (s *appTrafficState) bridgesForApp(appID string) []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]string(nil), s.apps[appID].Bridges...)
}

func (s *appTrafficState) limitForApp(appID string) AppTrafficLimit {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.limits[appID]
}

func (s *appTrafficState) setLimit(appID string, limit AppTrafficLimit) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if limit.UploadKbps == 0 && limit.DownloadKbps == 0 {
		delete(s.limits, appID)
	} else {
		limit.UpdatedAt = time.Now().Format(time.DateTime)
		s.limits[appID] = limit
	}
	return s.persistLocked(time.Now())
}

func (s *appTrafficState) limitsSnapshot() map[string]AppTrafficLimit {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]AppTrafficLimit, len(s.limits))
	for appID, limit := range s.limits {
		out[appID] = limit
	}
	return out
}

func (s *appTrafficState) flush() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.persistLocked(time.Now())
}

func (s *appTrafficState) persistLocked(now time.Time) error {
	persisted := appTrafficPersistedState{Apps: s.apps, Baselines: s.baselines, Limits: s.limits, LegacyBridges: s.legacyBridges}
	if err := writeJSONFile(s.path, persisted, true); err != nil {
		return err
	}
	s.lastPersist = now
	return nil
}
