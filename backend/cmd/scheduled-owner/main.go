package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/Wei-Shaw/sub2api/internal/custom/scheduledowner"
	"github.com/google/uuid"
	_ "github.com/lib/pq"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := run(ctx, os.Args[1:], os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}

type options struct {
	apply, status, importPlans, freeze, activate, acknowledge, review bool
	plan, epoch                                                       int64
	plans, owner, databaseID, reason, dsnPath                         string
}

func parseOptions(args []string, stdout io.Writer) (options, bool, error) {
	var opt options
	flags := flag.NewFlagSet("scheduled-owner", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.BoolVar(&opt.apply, "apply-schema", false, "显式创建独立 owner schema")
	flags.BoolVar(&opt.status, "status", false, "读取数据库标识和计划迁移摘要")
	flags.BoolVar(&opt.importPlans, "import", false, "保留历史并登记存量计划")
	flags.BoolVar(&opt.freeze, "freeze", false, "冻结新准入并等待既有执行排空")
	flags.BoolVar(&opt.activate, "activate", false, "排空后切换指定 owner 并递增 epoch")
	flags.BoolVar(&opt.acknowledge, "ack-unknown", false, "保留未知调用且不重放该 slot")
	flags.BoolVar(&opt.review, "review-edit", false, "冻结排空后显式接受审阅过的计划或题库变更")
	flags.Int64Var(&opt.plan, "plan", 0, "单个计划 ID")
	flags.Int64Var(&opt.epoch, "epoch", -1, "期望的当前 owner epoch")
	flags.StringVar(&opt.plans, "plans", "", "逗号分隔的计划 ID，最多 1000 个")
	flags.StringVar(&opt.owner, "owner", "", "切换目标 legacy 或 dispatch")
	flags.StringVar(&opt.databaseID, "database-id", "", "从 status 读回的精确数据库标识")
	flags.StringVar(&opt.reason, "reason", "", "未知调用的人工处置依据摘要")
	flags.StringVar(&opt.dsnPath, "dsn-file", "", "管理数据库 DSN 的受限文件")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 {
		if errors.Is(err, flag.ErrHelp) {
			_, _ = fmt.Fprintln(stdout, "scheduled-owner --apply-schema | --status [--plans <ids>] | --import --plans <ids> --database-id <id> | (--freeze | --activate --owner legacy|dispatch | --ack-unknown --reason <text> | --review-edit --reason <text>) --plan <id> --epoch <n> --database-id <id> [--dsn-file <path>]")
			return opt, false, nil
		}
		return opt, false, errors.New("scheduled-owner 参数无效")
	}
	count := 0
	for _, selected := range []bool{opt.apply, opt.status, opt.importPlans, opt.freeze, opt.activate, opt.acknowledge, opt.review} {
		if selected {
			count++
		}
	}
	if count == 0 {
		_, _ = fmt.Fprintln(stdout, "scheduled-owner 默认关闭；使用显式动作登记、冻结或切换计划")
		return opt, false, nil
	}
	if count != 1 {
		return opt, false, errors.New("scheduled-owner 只接受一个动作")
	}
	if opt.freeze || opt.activate || opt.acknowledge || opt.review {
		if opt.plan < 1 || opt.epoch < 0 || (!opt.acknowledge && opt.epoch == 0) || opt.plans != "" {
			return opt, false, errors.New("单计划动作必须提供 --plan 和 --epoch")
		}
	} else if opt.plan != 0 || opt.epoch != -1 {
		return opt, false, errors.New("该动作不接受 --plan 或 --epoch")
	}
	if opt.importPlans {
		if _, err := parsePlanIDs(opt.plans); err != nil {
			return opt, false, err
		}
	} else if opt.plans != "" {
		if !opt.status {
			return opt, false, errors.New("该动作不接受 --plans")
		}
		if _, err := parsePlanIDs(opt.plans); err != nil {
			return opt, false, err
		}
	}
	if !opt.apply && !opt.status {
		id, err := uuid.Parse(opt.databaseID)
		if err != nil || id == uuid.Nil || id.String() != opt.databaseID {
			return opt, false, errors.New("变更必须提供从 --status 读回的 --database-id")
		}
	} else if opt.databaseID != "" {
		return opt, false, errors.New("只读或 schema 动作不接受 --database-id")
	}
	if (opt.activate && opt.owner != "legacy" && opt.owner != "dispatch") || (!opt.activate && opt.owner != "") {
		return opt, false, errors.New("--owner 仅用于 --activate，且必须为 legacy 或 dispatch")
	}
	if opt.acknowledge || opt.review {
		if strings.TrimSpace(opt.reason) == "" || len(opt.reason) > 512 || !utf8.ValidString(opt.reason) || strings.ContainsAny(opt.reason, "\x00\r\n") {
			return opt, false, errors.New("处置未知调用或接受变更必须提供不超过 512 字节的单行 --reason")
		}
	} else if opt.reason != "" {
		return opt, false, errors.New("--reason 仅用于 --ack-unknown 或 --review-edit")
	}
	return opt, true, nil
}

func parsePlanIDs(raw string) ([]int64, error) {
	parts := strings.Split(raw, ",")
	if raw == "" || len(parts) > 1000 {
		return nil, errors.New("计划列表必须包含 1 至 1000 个唯一正整数")
	}
	ids := make([]int64, len(parts))
	seen := make(map[int64]bool, len(parts))
	for i, part := range parts {
		id, err := strconv.ParseInt(part, 10, 64)
		if err != nil || id < 1 || seen[id] || strconv.FormatInt(id, 10) != part {
			return nil, errors.New("计划列表必须包含 1 至 1000 个唯一正整数")
		}
		ids[i], seen[id] = id, true
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids, nil
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	opt, active, err := parseOptions(args, stdout)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 2
	}
	if !active {
		return 0
	}
	operationCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	db, err := openDB(operationCtx, opt.dsnPath)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "scheduled-owner 数据库连接失败")
		return 1
	}
	defer func() { _ = db.Close() }()
	if opt.apply {
		if err = scheduledowner.ApplySchema(operationCtx, db); err != nil {
			_, _ = fmt.Fprintln(stderr, "scheduled-owner schema 应用失败")
			return 1
		}
	}
	store := scheduledowner.NewStore(db)
	if err = store.CheckSchema(operationCtx); err != nil {
		_, _ = fmt.Fprintln(stderr, "scheduled-owner schema 不可用或版本不匹配")
		return 1
	}
	databaseID, err := store.DatabaseID(operationCtx)
	if err != nil || (opt.databaseID != "" && opt.databaseID != databaseID) {
		_, _ = fmt.Fprintln(stderr, "scheduled-owner schema 或数据库标识不匹配")
		return 1
	}
	store = store.ForDatabase(databaseID)
	var result any
	switch {
	case opt.apply:
		result = map[string]any{"database_id": databaseID, "schema_applied": true}
	case opt.status:
		var ids []int64
		if opt.plans != "" {
			ids, _ = parsePlanIDs(opt.plans)
		}
		result, err = store.Status(operationCtx, ids)
	case opt.importPlans:
		ids, _ := parsePlanIDs(opt.plans)
		result, err = store.Import(operationCtx, ids)
	case opt.freeze:
		result, err = store.Freeze(operationCtx, opt.plan, opt.epoch)
	case opt.activate:
		result, err = store.Activate(operationCtx, opt.plan, opt.epoch, opt.owner)
	case opt.acknowledge:
		result, err = store.AckUnknown(operationCtx, opt.plan, opt.epoch, strings.TrimSpace(opt.reason))
	case opt.review:
		result, err = store.Review(operationCtx, opt.plan, opt.epoch, strings.TrimSpace(opt.reason))
	}
	if err != nil {
		message := "scheduled-owner 操作失败；请核对冻结状态和 schema 权限"
		switch {
		case errors.Is(err, scheduledowner.ErrBusy):
			message = "scheduled-owner 计划仍持有执行锁；尚未排空"
		case errors.Is(err, scheduledowner.ErrPending):
			message = "scheduled-owner 存在未终结调用；须先核对执行进程及在途事实"
		case errors.Is(err, scheduledowner.ErrEpoch):
			message = "scheduled-owner epoch 已变化；请重新读取 --status"
		case errors.Is(err, scheduledowner.ErrChanged):
			message = "scheduled-owner 计划或题库与登记快照不一致"
		case errors.Is(err, scheduledowner.ErrDatabase):
			message = "scheduled-owner 数据库标识不匹配"
		}
		_, _ = fmt.Fprintln(stderr, message)
		return 1
	}
	if err := json.NewEncoder(stdout).Encode(map[string]any{"database_id": databaseID, "result": result}); err != nil {
		return 1
	}
	return 0
}

func openDB(ctx context.Context, explicitPath string) (*sql.DB, error) {
	dsn, path := os.Getenv("SCHEDULED_OWNER_DSN"), os.Getenv("SCHEDULED_OWNER_DSN_FILE")
	if explicitPath != "" {
		if dsn != "" || path != "" {
			return nil, errors.New("conflicting owner database sources")
		}
		path = explicitPath
	}
	if path != "" {
		if dsn != "" {
			return nil, errors.New("conflicting owner database sources")
		}
		root, err := os.OpenRoot(filepath.Dir(path))
		if err != nil {
			return nil, errors.New("owner database file unavailable")
		}
		defer func() { _ = root.Close() }()
		info, err := root.Lstat(filepath.Base(path))
		if err != nil || !info.Mode().IsRegular() {
			return nil, errors.New("owner database file must be regular")
		}
		file, err := root.Open(filepath.Base(path))
		if err != nil {
			return nil, errors.New("owner database file unavailable")
		}
		defer func() { _ = file.Close() }()
		info, err = file.Stat()
		if err != nil || !info.Mode().IsRegular() {
			return nil, errors.New("owner database file must be regular")
		}
		body, err := io.ReadAll(io.LimitReader(file, 16<<10+1))
		if err != nil || len(body) > 16<<10 {
			return nil, errors.New("owner database file read limit exceeded")
		}
		dsn = strings.TrimSpace(string(body))
	}
	if strings.TrimSpace(dsn) == "" {
		return nil, errors.New("owner database source required")
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, errors.New("owner database unavailable")
	}
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(2)
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, errors.New("owner database unavailable")
	}
	return db, nil
}
