package probe

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"net"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"
)

type traceHopState struct {
	Hop       int
	IP        string
	Location  string
	LatencyMS int64
	Responses int
}

func RunTrace(ctx context.Context, host string, maxHops int, onUpdate func(TraceResult)) TraceResult {
	result := TraceResult{
		Target:    host,
		Timestamp: localTimestamp(),
		Tool:      "mtr",
		Running:   true,
	}
	if host == "" {
		result.Error = "host required"
		result.Running = false
		return result
	}
	if _, err := net.LookupHost(host); err != nil {
		result.Error = "dns: " + err.Error()
		result.Running = false
		return result
	}
	if maxHops <= 0 || maxHops > 30 {
		maxHops = 20
	}

	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	if _, err := exec.LookPath("mtr"); err != nil {
		result.Error = "mtr not installed"
		result.Running = false
		return result
	}

	args := []string{"--raw", "--udp", "--interval", "1", "--no-dns", "--max-ttl", strconv.Itoa(maxHops), host}
	cmd := exec.CommandContext(ctx, "mtr", args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		result.Error = err.Error()
		result.Running = false
		return result
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		result.Error = err.Error()
		result.Running = false
		return result
	}

	states := map[int]*traceHopState{}
	maxSeenHop := 0
	lastEmit := time.Time{}

	emit := func() {
		result.Hops = buildTraceHops(ctx, states, maxSeenHop)
		if onUpdate != nil {
			onUpdate(cloneTraceResult(result))
		}
		lastEmit = time.Now()
	}

	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		rawHop, err := strconv.Atoi(fields[1])
		if err != nil || rawHop < 0 {
			continue
		}
		hopIndex := rawHop + 1
		if hopIndex > maxHops {
			continue
		}
		if hopIndex > maxSeenHop {
			maxSeenHop = hopIndex
		}
		state := states[hopIndex]
		if state == nil {
			state = &traceHopState{Hop: hopIndex}
			states[hopIndex] = state
		}

		switch fields[0] {
		case "x":
			// Probe emitted. Keeping the placeholder hop lets the UI show timeout rows.
		case "h":
			ip := strings.TrimSpace(fields[2])
			parsed := net.ParseIP(ip)
			if parsed == nil {
				continue
			}
			ip = parsed.String()
			if state.IP != ip {
				state.IP = ip
				state.Location = classifyIPLocation(ctx, ip)
			}
		case "p":
			latencyUS, err := strconv.ParseInt(fields[2], 10, 64)
			if err != nil {
				continue
			}
			latencyMS := latencyUS / 1000
			if latencyUS > 0 && latencyMS == 0 {
				latencyMS = 1
			}
			state.Responses++
			if state.Responses == 1 {
				state.LatencyMS = latencyMS
			} else {
				total := state.LatencyMS*int64(state.Responses-1) + latencyMS
				state.LatencyMS = total / int64(state.Responses)
			}
		default:
			continue
		}

		if time.Since(lastEmit) >= 250*time.Millisecond {
			emit()
		}
	}
	scanErr := scanner.Err()
	waitErr := cmd.Wait()

	if scanErr != nil && !errors.Is(scanErr, context.Canceled) {
		result.Error = scanErr.Error()
	}
	if waitErr != nil && ctx.Err() == nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = waitErr.Error()
		}
		result.Error = msg
	}
	if len(result.Hops) == 0 {
		result.Hops = buildTraceHops(ctx, states, maxSeenHop)
	}
	if len(result.Hops) == 0 && result.Error == "" {
		result.Error = "未采集到跳点，请确认容器具备 mtr 运行条件"
	}
	if onUpdate != nil {
		onUpdate(cloneTraceResult(result))
	}

	result.Running = false
	result.Finished = true
	return result
}

func buildTraceHops(ctx context.Context, states map[int]*traceHopState, maxSeenHop int) []TraceHop {
	if maxSeenHop <= 0 {
		return nil
	}
	keys := make([]int, 0, len(states))
	for hop := range states {
		keys = append(keys, hop)
	}
	sort.Ints(keys)

	hops := make([]TraceHop, 0, maxSeenHop)
	for hop := 1; hop <= maxSeenHop; hop++ {
		state, ok := states[hop]
		if !ok {
			hops = append(hops, TraceHop{Hop: hop})
			continue
		}
		traceHop := TraceHop{
			Hop:       state.Hop,
			IP:        state.IP,
			LatencyMS: state.LatencyMS,
			Location:  state.Location,
		}
		if traceHop.IP != "" && traceHop.Location == "" {
			traceHop.Location = classifyIPLocation(ctx, traceHop.IP)
		}
		hops = append(hops, traceHop)
	}
	return hops
}

func cloneTraceResult(in TraceResult) TraceResult {
	out := in
	if len(in.Hops) > 0 {
		out.Hops = append([]TraceHop(nil), in.Hops...)
	}
	return out
}
