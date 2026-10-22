// Copyright 2026 Antrea Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Command ipam-recovery monitors duplicate Pod IPs and optionally quarantines affected Nodes
// and evicts explicitly opted-in Deployment Pods. It never uncordons Nodes.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/klog/v2"
)

type monitorState struct {
	scanTime     atomic.Int64
	groups       atomic.Int64
	blocked      atomic.Int64
	waiting      atomic.Int64
	waitingSince atomic.Int64
	failures     atomic.Int64
}

func (s *monitorState) update(groups int, err error, now time.Time) {
	s.groups.Store(int64(groups))
	if err != nil && !isWaiting(err) {
		s.blocked.Store(1)
		s.waiting.Store(0)
		s.waitingSince.Store(0)
		s.failures.Add(1)
		return
	}
	s.blocked.Store(0)
	s.scanTime.Store(now.Unix())
	if isWaiting(err) {
		s.waiting.Store(1)
		s.waitingSince.CompareAndSwap(0, now.Unix())
	} else {
		s.waiting.Store(0)
		s.waitingSince.Store(0)
	}
}

func (s *monitorState) ready(now time.Time, interval time.Duration) bool {
	last := s.scanTime.Load()
	age := now.Unix() - last
	return last != 0 && age >= 0 && age <= int64((4*interval+time.Minute).Seconds())
}

func run() error {
	klog.InitFlags(nil)
	var cfg settings
	var kubeconfig, listen string
	var interval, requestTimeout time.Duration
	var once bool
	flag.StringVar(&kubeconfig, "kubeconfig", "", "Kubeconfig path; empty uses in-cluster credentials")
	flag.StringVar(&cfg.Namespace, "namespace", "antrea-ipam-recovery", "Namespace of the pre-created journal ConfigMap")
	flag.StringVar(&cfg.StateName, "state-name", "antrea-ipam-recovery-state", "Pre-created journal ConfigMap name")
	flag.BoolVar(&cfg.DryRun, "dry-run", true, "Monitor only; no cluster writes")
	flag.BoolVar(&once, "once", false, "Perform one monitor-only scan and exit")
	flag.DurationVar(&interval, "interval", 15*time.Second, "Scan interval (at least 5s)")
	flag.DurationVar(&requestTimeout, "request-timeout", 10*time.Second, "Timeout for each Kubernetes API request")
	flag.DurationVar(&cfg.Confirm, "confirm-duration", time.Minute, "Minimum unchanged conflict duration (at least 30s and 3 scans)")
	flag.DurationVar(&cfg.MinAge, "min-pod-age", 2*time.Minute, "Minimum victim Pod age (at least 1m)")
	flag.DurationVar(&cfg.Timeout, "recovery-timeout", 30*time.Minute, "Incident deadline; expiration requires manual intervention")
	flag.DurationVar(&cfg.Cooldown, "cooldown", 30*time.Minute, "Minimum interval between completed incidents (at least 5m)")
	flag.IntVar(&cfg.MaxDaily, "max-daily-recoveries", 3, "Maximum Node incidents per rolling 24h (1-10)")
	flag.IntVar(&cfg.MaxPods, "max-pods", 8, "Maximum victim Pods in one incident (2-32)")
	flag.StringVar(&listen, "listen-address", ":8080", "Health and Prometheus metrics listen address")
	flag.Parse()
	if flag.NArg() != 0 || interval < 5*time.Second || requestTimeout <= 0 || requestTimeout > time.Minute ||
		cfg.Confirm < 30*time.Second || cfg.MinAge < time.Minute || cfg.Timeout < time.Minute ||
		cfg.Cooldown < 5*time.Minute || cfg.MaxDaily < 1 || cfg.MaxDaily > 10 || cfg.MaxPods < 2 || cfg.MaxPods > 32 ||
		cfg.Namespace == "" || cfg.StateName == "" || (once && !cfg.DryRun) {
		return fmt.Errorf("invalid options; see --help for safety bounds (--once requires --dry-run=true)")
	}
	var config *rest.Config
	var err error
	if kubeconfig == "" {
		config, err = rest.InClusterConfig()
	} else {
		config, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
	}
	if err != nil {
		return err
	}
	config.Timeout = requestTimeout
	config.UserAgent = "antrea-ipam-recovery"
	config.QPS, config.Burst = 5, 10
	client, err := kubernetes.NewForConfig(config)
	if err != nil {
		return err
	}
	r := &recovery{client: client, settings: cfg, now: time.Now}
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer cancel()
	var status monitorState
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, req *http.Request) { w.WriteHeader(http.StatusOK) })
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, req *http.Request) {
		if !status.ready(time.Now(), interval) {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/metrics", func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		if _, err := fmt.Fprintf(w, "antrea_ipam_recovery_last_success_timestamp_seconds %d\n"+
			"antrea_ipam_recovery_duplicate_nodes %d\n"+
			"antrea_ipam_recovery_blocked %d\n"+
			"antrea_ipam_recovery_waiting %d\n"+
			"antrea_ipam_recovery_waiting_since_timestamp_seconds %d\n"+
			"antrea_ipam_recovery_reconcile_errors_total %d\n", status.scanTime.Load(), status.groups.Load(),
			status.blocked.Load(), status.waiting.Load(), status.waitingSince.Load(), status.failures.Load()); err != nil {
			klog.ErrorS(err, "Failed to write metrics")
		}
	})
	server := &http.Server{Addr: listen, Handler: mux, ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second, IdleTimeout: 30 * time.Second}
	serverError := make(chan error, 1)
	if !once {
		go func() { serverError <- server.ListenAndServe() }()
		defer func() {
			shutdownCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
			defer stop()
			if err := server.Shutdown(shutdownCtx); err != nil {
				klog.ErrorS(err, "Failed to stop HTTP server")
			}
		}()
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	klog.InfoS("Starting duplicate Pod IP monitor", "dryRun", cfg.DryRun, "namespace", cfg.Namespace)
	for {
		// Bound the whole reconciliation, including client-go retries and pagination.
		// Per-request HTTP timeouts alone do not bound a sequence of retries.
		scanCtx, stop := context.WithTimeout(ctx, time.Minute)
		groups, err := r.step(scanCtx)
		stop()
		status.update(groups, err, time.Now())
		if err != nil && !isWaiting(err) {
			klog.ErrorS(err, "Recovery blocked; no unsafe fallback will be attempted")
		} else if isWaiting(err) {
			klog.V(2).InfoS("Recovery is waiting for workload progress", "reason", err)
		}
		if once {
			return err
		}
		select {
		case <-ctx.Done():
			return nil
		case err := <-serverError:
			if errors.Is(err, http.ErrServerClosed) {
				return nil
			}
			return err
		case <-ticker.C:
		}
	}
}

func main() {
	if err := run(); err != nil {
		klog.ErrorS(err, "IPAM recovery stopped")
		klog.Flush()
		os.Exit(1)
	}
	klog.Flush()
}
