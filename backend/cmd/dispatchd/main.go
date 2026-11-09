package main

import (
	"context"
	"crypto/tls"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/custom/dispatch"
	_ "github.com/lib/pq"
	"github.com/redis/go-redis/v9"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := run(ctx, os.Args[1:], os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("dispatchd", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	continuous := flags.Bool("run", false, "显式运行合成调度控制面")
	once := flags.Bool("once", false, "执行一轮探测并发布快照")
	apply := flags.Bool("apply-schema", false, "显式应用独立 dispatch schema")
	applyQuality := flags.Bool("apply-quality-schema", false, "显式应用独立质量 schema")
	qualityReport := flags.Bool("quality-report", false, "输出独立字形与问答质量摘要")
	healthHolds := flags.Bool("health-holds", false, "读取持久隔离及其当前版本")
	healthReport := flags.Bool("health-report", false, "只读滚动健康窗口与健康因子")
	probeBudgetReport := flags.Bool("probe-budget-report", false, "只读探针成本上界预算与待闭合预留")
	metricsReport := flags.Bool("metrics-report", false, "只读分钟／小时聚合、聚合水位与历史保留现状")
	closeProbeBudget := flags.String("close-probe-budget-file", "", "按版本和处置依据确认未知探针按上界计额")
	releaseHold := flags.String("release-hold-file", "", "按带版本和处置依据的请求解除隔离并重新探活")
	rotateLineage := flags.Bool("rotate-publish-lineage", false, "物理恢复后显式轮换快照发布 lineage")
	configPath := flags.String("config", "", "运行时元数据配置文件")
	dsnPath := flags.String("dsn-file", "", "调度数据库 DSN 文件")
	observations := flags.String("observe-file", "", "导入可信脱敏观测回放 JSON 文件")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 {
		if errors.Is(err, flag.ErrHelp) {
			_, _ = fmt.Fprintln(stdout, "dispatchd --apply-schema | --apply-quality-schema | (--once | --run | --quality-report | --health-report | --health-holds | --probe-budget-report | --metrics-report | --close-probe-budget-file <path> | --release-hold-file <path> | --rotate-publish-lineage | --observe-file <path>) --config <path> [--dsn-file <path>]")
			return 0
		}
		_, _ = fmt.Fprintln(stderr, "dispatchd 参数无效")
		return 2
	}
	actions := 0
	for _, selected := range []bool{*continuous, *once, *apply, *applyQuality, *qualityReport, *healthReport, *healthHolds, *probeBudgetReport, *metricsReport, *closeProbeBudget != "", *releaseHold != "", *rotateLineage, *observations != ""} {
		if selected {
			actions++
		}
	}
	if actions == 0 {
		_, _ = fmt.Fprintln(stdout, "dispatchd 默认关闭；使用 --apply-schema、--apply-quality-schema、--once、--run、--quality-report、--health-report、--health-holds、--probe-budget-report、--metrics-report、--close-probe-budget-file、--release-hold-file、--rotate-publish-lineage 或 --observe-file 显式启用")
		return 0
	}
	if actions != 1 || (!*apply && !*applyQuality && *configPath == "") {
		_, _ = fmt.Fprintln(stderr, "dispatchd 只接受一个动作，工作动作必须提供 --config")
		return 2
	}
	operationCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	db, err := openDB(operationCtx, "DISPATCH", *dsnPath)
	cancel()
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "dispatchd 调度数据库连接失败")
		return 1
	}
	defer func() { _ = db.Close() }()
	if *apply || *applyQuality {
		operationCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		var err error
		if *applyQuality {
			err = dispatch.ApplyQualitySchema(operationCtx, db)
		} else {
			err = dispatch.ApplySchema(operationCtx, db)
		}
		cancel()
		if err != nil {
			_, _ = fmt.Fprintln(stderr, "dispatchd schema 应用失败")
			return 1
		}
		_, _ = fmt.Fprintln(stdout, "dispatchd schema 已应用")
		return 0
	}
	cfg, err := dispatch.LoadConfig(*configPath)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	store := dispatch.NewStore(db)
	if *rotateLineage {
		return runRotatePublishLineage(ctx, store, stdout, stderr)
	}
	if *probeBudgetReport || *closeProbeBudget != "" {
		return runProbeBudgetAction(ctx, store, cfg, *probeBudgetReport, *closeProbeBudget, stdout, stderr)
	}
	if *metricsReport {
		return runMetricsReport(ctx, store, cfg, stdout, stderr)
	}
	if *healthReport {
		reportCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		reports, err := store.HealthWindowReports(reportCtx, cfg.CoreInstance, cfg.ConfigRevision)
		if err != nil {
			_, _ = fmt.Fprintln(stderr, "dispatchd 健康报告不可用；核对配置版本、迁移及权限")
			return 1
		}
		if json.NewEncoder(stdout).Encode(reports) != nil {
			return 1
		}
		return 0
	}
	if *healthHolds || *releaseHold != "" {
		return runHealthHoldAction(ctx, store, cfg, *healthHolds, *releaseHold, stdout, stderr)
	}
	if *qualityReport {
		reportCtx, reportCancel := context.WithTimeout(ctx, 10*time.Second)
		reports, err := qualityReports(reportCtx, store, cfg)
		reportCancel()
		if err != nil {
			_, _ = fmt.Fprintln(stderr, "dispatchd 质量摘要不可用")
			return 1
		}
		if json.NewEncoder(stdout).Encode(reports) != nil {
			return 1
		}
		return 0
	}
	if *observations != "" {
		if err := importObservations(ctx, store, cfg, *observations); err != nil {
			_, _ = fmt.Fprintln(stderr, "dispatchd 观测导入失败")
			return 1
		}
		_, _ = fmt.Fprintln(stdout, "dispatchd 可信观测已导入；快照在下一工作轮次发布")
		return 0
	}
	var telemetry *dispatch.TelemetryReader
	if cfg.Telemetry != nil {
		telemetryCtx, telemetryCancel := context.WithTimeout(ctx, 10*time.Second)
		coreDB, err := openDB(telemetryCtx, "DISPATCH_CORE", "")
		if err != nil {
			telemetryCancel()
			_, _ = fmt.Fprintln(stderr, "dispatchd Core 观测只读来源不可用")
			return 1
		}
		defer func() { _ = coreDB.Close() }()
		passive, err := dispatch.NewPassiveReader(telemetryCtx, coreDB, cfg.CoreInstance, cfg.ConfigRevision)
		telemetryCancel()
		if err != nil {
			_, _ = fmt.Fprintln(stderr, "dispatchd Core 观测来源身份或权限无效")
			return 1
		}
		client, load, err := openLoadReader(*cfg.Telemetry)
		if err != nil {
			_, _ = fmt.Fprintln(stderr, "dispatchd Redis 负载来源配置无效")
			return 1
		}
		defer func() { _ = client.Close() }()
		telemetry = &dispatch.TelemetryReader{Passive: passive, Load: load}
	}
	if cfg.Quality != nil {
		qualityCtx, qualityCancel := context.WithTimeout(ctx, 10*time.Second)
		qualityDB, err := openDB(qualityCtx, "DISPATCH_QUALITY", "")
		if err != nil {
			qualityCancel()
			_, _ = fmt.Fprintln(stderr, "dispatchd 质量只读来源不可用")
			return 1
		}
		defer func() { _ = qualityDB.Close() }()
		source, err := dispatch.NewQualitySource(qualityCtx, qualityDB)
		qualityCancel()
		if err != nil {
			_, _ = fmt.Fprintln(stderr, "dispatchd 质量来源身份或权限无效")
			return 1
		}
		if telemetry == nil {
			telemetry = &dispatch.TelemetryReader{}
		}
		telemetry.Quality = source
	}
	if cfg.Scheduled != nil {
		scheduledCtx, scheduledCancel := context.WithTimeout(ctx, 10*time.Second)
		scheduledDB, err := openDB(scheduledCtx, "DISPATCH_SCHEDULED", "")
		if err != nil {
			scheduledCancel()
			_, _ = fmt.Fprintln(stderr, "dispatchd 定时任务队列来源不可用")
			return 1
		}
		defer func() { _ = scheduledDB.Close() }()
		source, err := dispatch.NewScheduledSource(scheduledCtx, scheduledDB, *cfg.Scheduled)
		scheduledCancel()
		if err != nil {
			_, _ = fmt.Fprintln(stderr, "dispatchd 定时任务来源身份或权限无效")
			return 1
		}
		if telemetry == nil {
			telemetry = &dispatch.TelemetryReader{}
		}
		telemetry.Scheduled = source
	}
	var runner *dispatch.ProbeRunner
	if cfg.Probe != nil {
		credentials, err := dispatch.LoadProbeCredentials(cfg.Probe.CredentialsFile, cfg.Probe.Audience)
		if err != nil {
			_, _ = fmt.Fprintln(stderr, err)
			return 1
		}
		shadowCtx, shadowCancel := context.WithTimeout(ctx, 10*time.Second)
		shadowDB, err := openDB(shadowCtx, "DISPATCH_SHADOW", "")
		if err != nil {
			shadowCancel()
			_, _ = fmt.Fprintln(stderr, "dispatchd shadow 数据库连接失败")
			return 1
		}
		defer func() { _ = shadowDB.Close() }()
		reader, err := dispatch.NewShadowReceiptReader(shadowCtx, shadowDB, cfg.Probe.ReceiptSource)
		shadowCancel()
		if err != nil {
			_, _ = fmt.Fprintln(stderr, "dispatchd shadow 身份不可用")
			return 1
		}
		runner, err = dispatch.NewProbeRunner(*cfg.Probe, credentials, reader)
		if err != nil {
			_, _ = fmt.Fprintln(stderr, err)
			return 1
		}
		defer runner.Close()
	}
	if cfg.SnapshotServer != nil {
		checkCtx, checkCancel := context.WithTimeout(ctx, 10*time.Second)
		err := store.CheckPublishLineageSchema(checkCtx)
		checkCancel()
		if err != nil {
			_, _ = fmt.Fprintln(stderr, "dispatchd 快照订阅需要 005 迁移；先执行 --apply-schema")
			return 1
		}
	}
	monitoring := newMonitor(cfg, store)
	if *continuous {
		stderr = continuousStderr(cfg, monitoring, stderr)
	}
	var server *dispatch.SnapshotServer
	if cfg.SnapshotServer != nil && *continuous {
		var stopServer func()
		server, stopServer, err = startSnapshotServer(ctx, cfg, store, stderr)
		if err != nil {
			_, _ = fmt.Fprintln(stderr, "dispatchd 快照订阅服务不可用；核对套接字路径与密钥文件")
			return 1
		}
		defer stopServer()
	}
	if monitoring != nil && *continuous && cfg.Monitoring.MetricsListen != "" {
		stopMetrics, err := serveMetrics(cfg.Monitoring.MetricsListen, monitoring.metrics)
		if err != nil {
			_, _ = fmt.Fprintln(stderr, "dispatchd 指标监听失败")
			return 1
		}
		defer stopMetrics()
	}
	encoder := json.NewEncoder(stdout)
	return runRounds(ctx, *once, time.Duration(cfg.PollIntervalMilliseconds)*time.Millisecond, monitoring, stderr, func() (int, bool) {
		roundCtx, roundCancel := context.WithTimeout(ctx, 2*time.Minute)
		started := time.Now()
		result, err := dispatch.RunRound(roundCtx, cfg, store, runner, telemetry)
		roundCancel()
		if monitoring != nil {
			monitoring.metrics.RecordRound(result, err, time.Since(started), time.Now())
		}
		if err != nil {
			if ctx.Err() != nil {
				return 0, true
			}
			_, _ = fmt.Fprintln(stderr, "dispatchd 工作轮次失败；现有快照不延长有效期")
			if *once {
				return 1, true
			}
		} else {
			if published, ok := result.Published(); ok && server != nil && server.Publish(published) != nil {
				_, _ = fmt.Fprintln(stderr, "dispatchd 快照订阅发布失败；订阅端保留旧快照直至到期")
			}
			if encoder.Encode(result) != nil {
				return 1, true
			}
		}
		return 0, false
	})
}

func openDB(ctx context.Context, prefix, explicitPath string) (*sql.DB, error) {
	dsn := os.Getenv(prefix + "_DSN")
	path := os.Getenv(prefix + "_DSN_FILE")
	if explicitPath != "" {
		if dsn != "" || path != "" {
			return nil, errors.New("conflicting dispatch DSN sources")
		}
		path = explicitPath
	}
	if path != "" {
		if dsn != "" {
			return nil, errors.New("conflicting dispatch DSN sources")
		}
		if err := readRuntimeSecret(path, &dsn); err != nil {
			return nil, err
		}
	}
	if dsn == "" {
		return nil, errors.New("dispatch DSN required")
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, errors.New("dispatch database unavailable")
	}
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(2)
	db.SetConnMaxLifetime(5 * time.Minute)
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, errors.New("dispatch database unavailable")
	}
	return db, nil
}

func importObservations(ctx context.Context, store *dispatch.Store, cfg dispatch.Config, path string) error {
	observations, err := dispatch.LoadObservations(path, cfg.CoreInstance)
	if err != nil {
		return err
	}
	operationCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := store.SyncRuntimeConfig(operationCtx, cfg); err != nil {
		return err
	}
	for _, observation := range observations {
		if _, err := store.Observe(operationCtx, cfg.CoreInstance, observation); err != nil {
			return err
		}
	}
	return nil
}

func openLoadReader(cfg dispatch.TelemetryConfig) (*redis.Client, *dispatch.RedisLoadReader, error) {
	url, path := os.Getenv("DISPATCH_REDIS_URL"), os.Getenv("DISPATCH_REDIS_URL_FILE")
	if path != "" {
		if url != "" {
			return nil, nil, errors.New("conflicting dispatch Redis sources")
		}
		var value string
		if err := readRuntimeSecret(path, &value); err != nil {
			return nil, nil, err
		}
		url = value
	}
	if strings.TrimSpace(url) == "" {
		return nil, nil, errors.New("dispatch Redis URL required")
	}
	options, err := redis.ParseURL(url)
	if err != nil {
		return nil, nil, errors.New("invalid dispatch Redis URL")
	}
	options.MaxRetries = -1
	options.DialTimeout = time.Second
	options.ReadTimeout = time.Second
	options.WriteTimeout = time.Second
	options.ContextTimeoutEnabled = true
	options.PoolSize = 4
	if options.TLSConfig != nil {
		options.TLSConfig.MinVersion = tls.VersionTLS13
	}
	client := redis.NewClient(options)
	reader, err := dispatch.NewRedisLoadReader(client, cfg.SlotTTLSeconds)
	if err != nil {
		_ = client.Close()
		return nil, nil, err
	}
	return client, reader, nil
}

func readRuntimeSecret(path string, output *string) error {
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return errors.New("dispatch source file unavailable")
	}
	defer func() { _ = root.Close() }()
	info, err := root.Lstat(filepath.Base(path))
	if err != nil || !info.Mode().IsRegular() {
		return errors.New("dispatch source file must be regular")
	}
	file, err := root.Open(filepath.Base(path))
	if err != nil {
		return errors.New("dispatch source file unavailable")
	}
	defer func() { _ = file.Close() }()
	info, err = file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return errors.New("dispatch source file must be regular")
	}
	body, err := io.ReadAll(io.LimitReader(file, 16<<10+1))
	if err != nil || len(body) > 16<<10 {
		return errors.New("dispatch source file read limit exceeded")
	}
	*output = strings.TrimSpace(string(body))
	return nil
}
