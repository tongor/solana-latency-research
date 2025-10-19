package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/pflag"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"solana-latency-research/internal/metrics"
	"solana-latency-research/internal/utils"
)

func main() {
	logger := log.New(os.Stdout, "[solana-latency] ", log.LstdFlags|log.Lmicroseconds)

	flags := pflag.NewFlagSet(os.Args[0], pflag.ExitOnError)
	configPath := flags.String("config", "configs/config.example.yaml", "配置文件路径")
	overrideGRPC := flags.String("grpc-endpoint", "", "覆盖 gRPC 端点地址")
	overrideAccount := flags.StringSlice("account", nil, "覆盖订阅账户，逗号分隔")

	if err := flags.Parse(os.Args[1:]); err != nil {
		logger.Fatalf("解析参数失败: %v", err)
	}

	cfg, err := utils.LoadConfig(*configPath)
	if err != nil {
		logger.Fatalf("加载配置失败: %v", err)
	}

	if flags.Changed("grpc-endpoint") && *overrideGRPC != "" {
		cfg.GRPC = *overrideGRPC
	}
	if flags.Changed("account") && len(*overrideAccount) > 0 {
		cfg.Filters.Accounts = *overrideAccount
	}

	logger.Printf("启动 Solana 延迟监测，gRPC 端点: %s", cfg.GRPC)

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	col := metrics.NewCollector()
	if cfg.Metrics.PrometheusPort > 0 {
		go exposeMetrics(ctx, cfg.Metrics.PrometheusPort, col, logger)
	}

	conn, err := dialWithRetry(ctx, cfg.GRPC, cfg.Reconnect, logger)
	if err != nil {
		logger.Fatalf("连接 Yellowstone gRPC 失败: %v", err)
	}
	defer conn.Close()

	logger.Printf("gRPC 连接已建立，等待订阅流启动")

	go monitorSlots(ctx, cfg.Interval, col, logger)
	go monitorTransactions(ctx, cfg.Interval, cfg.Filters.Accounts, col, logger)

	<-ctx.Done()
	logger.Println("收到退出信号，准备关闭")
}

func dialWithRetry(ctx context.Context, endpoint string, retryCfg utils.RetryConfig, logger *log.Logger) (*grpc.ClientConn, error) {
	if retryCfg.Backoff <= 0 {
		retryCfg.Backoff = 2 * time.Second
	}

	var attempt int
	for {
		attempt++
		conn, err := grpc.DialContext(
			ctx,
			endpoint,
			grpc.WithTransportCredentials(insecure.NewCredentials()),
			grpc.WithBlock(),
			grpc.WithReturnConnectionError(),
		)
		if err == nil {
			return conn, nil
		}

		logger.Printf("第 %d 次连接失败: %v", attempt, err)
		if retryCfg.Retries >= 0 && attempt > retryCfg.Retries {
			return nil, fmt.Errorf("超过最大重试次数 (%d): %w", retryCfg.Retries, err)
		}

		select {
		case <-ctx.Done():
			return nil, errors.Join(err, ctx.Err())
		case <-time.After(retryCfg.Backoff):
		}
	}
}

func exposeMetrics(ctx context.Context, port int, col *metrics.Collector, logger *log.Logger) {
	addr := fmt.Sprintf(":%d", port)
	mux := http.NewServeMux()
	mux.Handle("/metrics", col.Handler())

	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			logger.Printf("关闭 Prometheus 服务时出错: %v", err)
		}
	}()

	logger.Printf("Prometheus 指标监听端口 %s", addr)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Printf("Prometheus 服务异常终止: %v", err)
	}
}

func monitorSlots(ctx context.Context, interval time.Duration, col *metrics.Collector, logger *log.Logger) {
	// TODO: 替换为真实的 Yellowstone SlotUpdate 订阅，当前使用定时器模拟。
	if interval <= 0 {
		interval = 5 * time.Second
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	var previous time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			if !previous.IsZero() {
				delta := now.Sub(previous).Seconds()
				col.SlotInterval.Observe(delta)
				logJSON(logger, map[string]any{
					"stream":            "slot",
					"interval_seconds":  delta,
					"timestamp":         now.Format(time.RFC3339Nano),
					"note":              "定时器模拟 slot 更新，替换为 Geyser SubscribeSlots 后可移除",
					"next_leader_pubkey": "",
				})
			}
			previous = now
		}
	}
}

func monitorTransactions(ctx context.Context, interval time.Duration, accounts []string, col *metrics.Collector, logger *log.Logger) {
	// TODO: 替换为真实的 Yellowstone TransactionSubscribe 流。
	if interval <= 0 {
		interval = 5 * time.Second
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	rng := rand.New(rand.NewSource(time.Now().UnixNano()))

	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			latency := rng.Float64()*1.5 + 0.1
			col.TransactionDelay.Observe(latency)
			logJSON(logger, map[string]any{
				"stream":          "transaction",
				"latency_seconds": latency,
				"timestamp":       now.Format(time.RFC3339Nano),
				"publisher":       "simulated",
				"accounts":        accounts,
				"note":            "定时器模拟交易确认延迟，替换为真实 Yellowstone 数据后可移除",
			})
		}
	}
}

func logJSON(logger *log.Logger, payload map[string]any) {
	data, err := json.Marshal(payload)
	if err != nil {
		logger.Printf("编码 JSON 日志失败: %v", err)
		return
	}
	logger.Printf("%s", data)
}
