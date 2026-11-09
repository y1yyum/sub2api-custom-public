package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"runtime"
	"strconv"
	"syscall"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/custombridge/edge"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := run(ctx, os.Args[1:], os.Stdout, os.Stderr)
	cancel()
	os.Exit(code)
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("bridge-edge", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	enabled := flags.Bool("run", false, "显式启用 Edge 监听")
	configPath := flags.String("config", "", "运行时 JSON 配置路径")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 {
		if err == flag.ErrHelp {
			_, _ = fmt.Fprintln(stdout, "bridge-edge --run --config <运行时配置路径>")
			_, _ = fmt.Fprintln(stdout, "启用 shadow_dispatch 后，stdout 在启动、每 30 秒和正常退出时输出 bridge_edge_shadow_stats JSONL 聚合")
			_, _ = fmt.Fprintln(stdout, "启用 strict_dispatch 后输出相同阶段的 bridge_edge_strict_stats JSONL 聚合；与 shadow_dispatch 互斥")
			_, _ = fmt.Fprintln(stdout, "调度统计要求 Linux 可设置写期限的 stdout 管道；普通文件或其他不支持的输出启动失败")
			return 0
		}
		_, _ = fmt.Fprintln(stderr, "bridge-edge 参数无效；使用 --run --config <运行时配置路径>")
		return 2
	}
	if !*enabled {
		_, _ = fmt.Fprintln(stdout, "bridge-edge 默认关闭；使用 --run --config 显式启动")
		return 0
	}
	if *configPath == "" {
		_, _ = fmt.Fprintln(stderr, "bridge-edge 启用时必须指定 --config")
		return 2
	}
	cfg, err := edge.LoadConfig(*configPath)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	var output *os.File
	if cfg.ShadowDispatch != nil || cfg.StrictDispatch != nil {
		output, err = openStatsOutput(stdout)
		if err != nil {
			_, _ = fmt.Fprintln(stderr, err)
			return 1
		}
		defer func() { _ = output.Close() }()
	}
	if err := edge.RunWithStats(ctx, cfg, output); err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}

func openStatsOutput(output io.Writer) (*os.File, error) {
	unsupported := errors.New("edge 调度统计要求 Linux 可设置写期限的 stdout 管道")
	file, ok := output.(*os.File)
	if !ok || file == nil || runtime.GOOS != "linux" {
		return nil, unsupported
	}
	// Linux O_NONBLOCK；运行时平台检查保留其他平台的可编译性。
	const linuxOpenNonblock = 0x800
	raw, err := file.SyscallConn()
	if err != nil {
		return nil, unsupported
	}
	var owned *os.File
	var openErr error
	err = raw.Control(func(fd uintptr) {
		owned, openErr = os.OpenFile("/proc/self/fd/"+strconv.FormatUint(uint64(fd), 10), os.O_WRONLY|os.O_APPEND|linuxOpenNonblock, 0)
	})
	if err != nil || openErr != nil {
		if owned != nil {
			_ = owned.Close()
		}
		return nil, unsupported
	}
	if err := owned.SetWriteDeadline(time.Now()); err != nil {
		_ = owned.Close()
		return nil, unsupported
	}
	if err := owned.SetWriteDeadline(time.Time{}); err != nil {
		_ = owned.Close()
		return nil, unsupported
	}
	return owned, nil
}
