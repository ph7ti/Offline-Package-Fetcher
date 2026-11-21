package inspector

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"opf/internal/core"
)

// CollectHostInfo collects non-secret host facts for session reports.
func (i *Inspector) CollectHostInfo(ctx context.Context) (core.HostInfo, error) {
	if i.tm == nil {
		return core.HostInfo{}, errors.New("transferência SSH não configurada")
	}
	info := core.HostInfo{}
	run := func(command string) string {
		if err := ctx.Err(); err != nil {
			return ""
		}
		out, err := i.tm.RunCommand(ctx, command)
		if err != nil {
			return ""
		}
		return strings.TrimSpace(out)
	}

	info.Usuario = run("id -un")
	info.Kernel = run("uname -r")
	if raw := run("cat /proc/uptime"); raw != "" {
		if fields := strings.Fields(raw); len(fields) > 0 {
			if seconds, err := strconv.ParseFloat(fields[0], 64); err == nil {
				d := time.Duration(seconds) * time.Second
				days := int(d.Hours()) / 24
				hours := int(d.Hours()) % 24
				minutes := int(d.Minutes()) % 60
				info.Uptime = fmt.Sprintf("%dd %dh %dm", days, hours, minutes)
			}
		}
	}
	cores := run("nproc")
	model := run(`awk -F: '/model name/{gsub(/^ +| +$/, "", $2); print $2; exit}' /proc/cpuinfo`)
	info.CPU = strings.TrimSpace(cores + " cores • " + model)
	info.Memoria = run(`awk '/MemTotal/{t=$2} /MemAvailable/{a=$2} END{printf "%.0f MB total, %.0f MB disponível", t/1024, a/1024}' /proc/meminfo`)
	info.Disco = run(`df -h / | awk 'NR==2{print $2" total, "$4" livre"}'`)
	return info, nil
}
