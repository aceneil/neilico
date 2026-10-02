package metrics

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"neilico/agent/internal/client"
)

type Counters struct {
	RX uint64
	TX uint64
}

type CounterReader interface {
	Counters(string) (Counters, error)
}

type SysfsReader struct {
	Root string
}

func (r SysfsReader) Counters(name string) (Counters, error) {
	root := r.Root
	if root == "" {
		root = "/sys/class/net"
	}
	rx, err := readCounter(filepath.Join(root, name, "statistics", "rx_bytes"))
	if err != nil {
		return Counters{}, err
	}
	tx, err := readCounter(filepath.Join(root, name, "statistics", "tx_bytes"))
	if err != nil {
		return Counters{}, err
	}
	return Counters{RX: rx, TX: tx}, nil
}

func readCounter(path string) (uint64, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	return strconv.ParseUint(strings.TrimSpace(string(data)), 10, 64)
}

type TrafficReporter struct {
	Reader    CounterReader
	Interface string
	Client    *client.Client
	NodeID    string
	Metrics   *Metrics
	Interval  time.Duration
}

func (r *TrafficReporter) Run(ctx context.Context) {
	if r.Reader == nil {
		r.Reader = SysfsReader{}
	}
	interval := r.Interval
	if interval <= 0 {
		interval = 60 * time.Second
	}
	timer := time.NewTimer(0)
	defer timer.Stop()
	var previous Counters
	havePrevious := false
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		current, err := r.Reader.Counters(r.Interface)
		if err == nil {
			if havePrevious {
				entries := []client.TrafficInput{}
				if current.RX >= previous.RX {
					entries = append(entries, client.TrafficInput{Direction: "in", Bytes: int64(current.RX - previous.RX), Protocol: "wireguard", Peer: "interface:" + r.Interface})
				}
				if current.TX >= previous.TX {
					entries = append(entries, client.TrafficInput{Direction: "out", Bytes: int64(current.TX - previous.TX), Protocol: "wireguard", Peer: "interface:" + r.Interface})
				}
				if len(entries) > 0 && r.Client != nil && r.NodeID != "" {
					if err := r.Client.ReportTraffic(ctx, r.NodeID, entries); err != nil {
						if r.Metrics != nil {
							r.Metrics.TrafficReport("failure")
						}
					} else {
						if r.Metrics != nil {
							r.Metrics.TrafficReport("success")
							for _, entry := range entries {
								r.Metrics.AddTraffic(entry.Direction, entry.Bytes)
							}
						}
					}
				}
			}
			previous = current
			havePrevious = true
		}
		timer.Reset(interval)
	}
}
