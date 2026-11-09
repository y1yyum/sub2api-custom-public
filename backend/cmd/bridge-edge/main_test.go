package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/custom/dispatch"
	"github.com/Wei-Shaw/sub2api/internal/custombridge/edge"
)

func TestBridgeEdgeDefaultDoesNotReadConfiguration(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), []string{"--config", filepath.Join(t.TempDir(), "absent.json")}, &stdout, &stderr)
	if code != 0 || stderr.Len() != 0 || !strings.Contains(stdout.String(), "默认关闭") {
		t.Fatal("默认关闭时仍读取配置或启动失败")
	}
}

func TestBridgeEdgeHelpDescribesShadowStats(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run(context.Background(), []string{"--help"}, &stdout, &stderr); code != 0 || stderr.Len() != 0 {
		t.Fatal("帮助命令失败")
	}
	if !strings.Contains(stdout.String(), "shadow_dispatch") || !strings.Contains(stdout.String(), "30 秒") || !strings.Contains(stdout.String(), "bridge_edge_shadow_stats") {
		t.Fatal("帮助未说明运行时统计输出")
	}
	if !strings.Contains(stdout.String(), "strict_dispatch") || !strings.Contains(stdout.String(), "bridge_edge_strict_stats") {
		t.Fatal("帮助未说明 strict 统计")
	}
}

func TestBridgeEdgeRunRequiresExplicitConfiguration(t *testing.T) {
	for _, args := range [][]string{{"--run"}, {"--run", "--config", "missing-edge-fixture.json"}, {"--unknown"}, {"unexpected"}} {
		var stdout, stderr bytes.Buffer
		if code := run(context.Background(), args, &stdout, &stderr); code == 0 {
			t.Fatal("错误启动参数被接受")
		}
	}
}

func TestBridgeEdgeInvalidConfigDoesNotLeakValues(t *testing.T) {
	path := filepath.Join(t.TempDir(), "edge.json")
	secret := "command-fixture-secret-do-not-log"
	if err := os.WriteFile(path, []byte(`{"version":"`+secret+`"}`), 0600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := run(context.Background(), []string{"--run", "--config", path}, &stdout, &stderr); code != 1 || strings.Contains(stderr.String(), secret) {
		t.Fatal("配置错误泄漏内容或状态不正确")
	}
}

func TestBridgeEdgeRunOutputsShadowStats(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("CLI 可取消统计适配器仅支持 Linux")
	}
	for _, mode := range []string{"disabled", "shadow", "strict"} {
		t.Run(mode, func(t *testing.T) {
			secret := "command-stats-fixture-secret-32-bytes"
			digest := sha256.Sum256([]byte("command-stats-bearer"))
			cfg := edge.Config{
				Version: 1, Listen: "127.0.0.1:0", CoreOrigin: "https://core.test", Audience: "core-fixture",
				SigningKeys: []edge.KeyConfig{{ID: "signing-fixture", SecretBase64: base64.StdEncoding.EncodeToString([]byte(secret)), InternalKeyID: 101, GroupID: 7}},
				Grants: []edge.Grant{{ID: "grant-fixture", BearerSHA256: hex.EncodeToString(digest[:]), SigningKeyID: "signing-fixture",
					AccountID: 42, CoreKeyID: 101, GroupID: 7, CoreAPIKey: "command-stats-core-key", PoolRef: "pool-fixture",
					PolicyRevision: "policy-fixture", PayerRef: "payer-fixture", ActorRef: "actor-fixture", Region: "region-fixture"}},
			}
			if mode != "disabled" {
				cfg.ShadowDispatch = &edge.ShadowDispatchConfig{SnapshotFile: filepath.Join(t.TempDir(), secret), CoreInstance: "core-fixture"}
			}
			if mode == "strict" {
				cfg.StrictDispatch, cfg.ShadowDispatch = cfg.ShadowDispatch, nil
				cfg.Grants[0].StrictTargets = []dispatch.Permit{{CandidateRef: "candidate-fixture", AccountID: 7, CredentialRef: "credential-fixture", CredentialRevision: 1, ProviderRef: "provider-fixture", Model: "fixture-model", MaxInflight: 1}}
			}
			body, err := json.Marshal(cfg)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(t.TempDir(), "edge.json")
			if err := os.WriteFile(path, body, 0600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			reader, writer, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = reader.Close(); _ = writer.Close() }()
			var stderr bytes.Buffer
			if code := run(ctx, []string{"--run", "--config", path}, writer, &stderr); code != 0 || stderr.Len() != 0 {
				t.Fatalf("正常关闭失败: code=%d stderr=%s", code, stderr.String())
			}
			if _, err := writer.Stat(); err != nil {
				t.Fatal("CLI 关闭了调用方输出")
			}
			_ = writer.Close()
			output, err := io.ReadAll(reader)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "disabled" {
				if len(output) != 0 {
					t.Fatal("未配置 shadow 时新增了输出")
				}
				return
			}
			raw := string(output)
			for _, value := range []string{secret, "command-stats-bearer", cfg.Grants[0].BearerSHA256, "command-stats-core-key", "pool-fixture", "payer-fixture", "actor-fixture", "grant-fixture", "region-fixture"} {
				if strings.Contains(raw, value) {
					t.Fatal("CLI 统计泄漏敏感或高基数字段")
				}
			}
			decoder := json.NewDecoder(bytes.NewReader(output))
			for _, phase := range []string{"shutdown"} {
				var record struct {
					Type  string           `json:"type"`
					Phase string           `json:"phase"`
					Stats edge.ShadowStats `json:"stats"`
				}
				if err := decoder.Decode(&record); err != nil {
					t.Fatalf("CLI 缺少 %s 统计: %v", phase, err)
				}
				if record.Type != "bridge_edge_"+mode+"_stats" || record.Phase != phase || !record.Stats.Enabled || record.Stats.RefreshFailures == 0 {
					t.Fatalf("CLI 聚合记录错误: %+v", record)
				}
			}
			if err := decoder.Decode(new(any)); err != io.EOF {
				t.Fatalf("存在多余输出: %v", err)
			}
		})
	}
}

func statsCommandConfig(t *testing.T, listen string) string {
	t.Helper()
	digest := sha256.Sum256([]byte("command-stats-bearer"))
	cfg := edge.Config{
		Version: 1, Listen: listen, CoreOrigin: "https://core.test", Audience: "core-fixture",
		SigningKeys:    []edge.KeyConfig{{ID: "signer", SecretBase64: base64.StdEncoding.EncodeToString([]byte(strings.Repeat("x", 32))), InternalKeyID: 101, GroupID: 7}},
		Grants:         []edge.Grant{{ID: "grant-fixture", BearerSHA256: hex.EncodeToString(digest[:]), SigningKeyID: "signer", AccountID: 42, CoreKeyID: 101, GroupID: 7, CoreAPIKey: "fixture-core-key", PoolRef: "pool-fixture", PolicyRevision: "policy-fixture", PayerRef: "payer-fixture", ActorRef: "actor-fixture", Region: "region-fixture"}},
		ShadowDispatch: &edge.ShadowDispatchConfig{SnapshotFile: filepath.Join(t.TempDir(), "missing.json"), CoreInstance: "core-fixture"},
	}
	body, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "edge.json")
	if err := os.WriteFile(path, body, 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

type noncancelableStatsWriter struct{ writes atomic.Int64 }

func (w *noncancelableStatsWriter) Write([]byte) (int, error) { w.writes.Add(1); select {} }

func TestBridgeEdgeRejectsUnsupportedStatsOutput(t *testing.T) {
	writer := &noncancelableStatsWriter{}
	if output, err := openStatsOutput(writer); err == nil {
		_ = output.Close()
		t.Fatal("接受了不可取消的普通 writer")
	}
	if writer.writes.Load() != 0 {
		t.Fatal("调用了不可取消的 Write")
	}
	file, err := os.CreateTemp(t.TempDir(), "regular-output-")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var stderr bytes.Buffer
	if code := run(ctx, []string{"--run", "--config", statsCommandConfig(t, "127.0.0.1:0")}, file, &stderr); code != 1 || !strings.Contains(stderr.String(), "统计要求") {
		t.Fatalf("普通文件没有显式报 unsupported: code=%d stderr=%s", code, stderr.String())
	}
	info, err := file.Stat()
	if err != nil || info.Size() != 0 {
		t.Fatal("关闭或写入了不支持的调用方文件")
	}
}

// 通过真实 exec 继承 fd 1，覆盖 os.Stdout 与 os.Pipe 工厂不同的 pollability。
func TestBridgeEdgeInheritedStdoutHelper(t *testing.T) {
	mode := os.Getenv("SUB2API_EDGE_STDOUT_HELPER")
	if mode == "" {
		return
	}
	control := os.NewFile(3, "test-control")
	if control == nil {
		os.Exit(91)
	}
	if !errors.Is(os.Stdout.SetWriteDeadline(time.Now()), os.ErrNoDeadline) {
		_, _ = fmt.Fprintln(control, "unexpected inherited stdout capability")
		os.Exit(92)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM)
	code := run(ctx, []string{"--run", "--config", os.Getenv("SUB2API_EDGE_STDOUT_CONFIG")}, os.Stdout, os.Stderr)
	cancel()
	if _, err := os.Stdout.Stat(); err != nil {
		_, _ = fmt.Fprintln(control, "original stdout closed")
		os.Exit(93)
	}
	if !errors.Is(os.Stdout.SetWriteDeadline(time.Now()), os.ErrNoDeadline) {
		_, _ = fmt.Fprintln(control, "original stdout capability changed")
		os.Exit(94)
	}
	_, _ = fmt.Fprintln(control, "original stdout open; no original deadline")
	os.Exit(code)
}

func TestBridgeEdgeInheritedStdoutNormalAndFullCancellation(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("生产 stdout 适配器仅 Linux")
	}
	for _, full := range []bool{false, true} {
		t.Run(map[bool]string{false: "readable", true: "full_pipe"}[full], func(t *testing.T) {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			address := listener.Addr().String()
			_ = listener.Close()
			reader, writer, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = reader.Close(); _ = writer.Close() }()
			if full {
				if err := writer.SetWriteDeadline(time.Now().Add(20 * time.Millisecond)); err != nil {
					t.Fatal(err)
				}
				if _, err := writer.Write(make([]byte, 1<<20)); !os.IsTimeout(err) {
					t.Fatalf("未填满 stdout 管道: %v", err)
				}
				_ = writer.SetWriteDeadline(time.Time{})
			}
			controlRead, controlWrite, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = controlRead.Close(); _ = controlWrite.Close() }()
			var stderr bytes.Buffer
			executable, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			command := exec.Command(executable, "-test.run=^TestBridgeEdgeInheritedStdoutHelper$")
			command.Env = append(os.Environ(), "SUB2API_EDGE_STDOUT_HELPER=child", "SUB2API_EDGE_STDOUT_CONFIG="+statsCommandConfig(t, address), "GORACE=atexit_sleep_ms=0")
			command.Stdout, command.Stderr = writer, &stderr
			command.ExtraFiles = []*os.File{controlWrite}
			if err := command.Start(); err != nil {
				t.Fatal(err)
			}
			_ = writer.Close()
			_ = controlWrite.Close()
			done := make(chan struct{})
			var waitErr error
			go func() { defer close(done); waitErr = command.Wait() }()
			defer func() { _ = command.Process.Kill(); <-done }()
			buffered := bufio.NewReader(reader)
			if !full {
				if err := reader.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
					t.Fatal(err)
				}
				line, err := buffered.ReadBytes('\n')
				if err != nil {
					t.Fatalf("继承 stdout 没有实际启动统计: %v", err)
				}
				if !bytes.Contains(line, []byte(`"phase":"startup"`)) || !bytes.Contains(line, []byte(`"type":"bridge_edge_shadow_stats"`)) {
					t.Fatalf("启动统计错误: %s", line)
				}
			} else {
				transport := &http.Transport{DisableKeepAlives: true, DialContext: (&net.Dialer{Timeout: 200 * time.Millisecond}).DialContext}
				defer transport.CloseIdleConnections()
				client := &http.Client{Transport: transport, Timeout: 200 * time.Millisecond}
				deadline := time.Now().Add(3 * time.Second)
				for {
					response, err := client.Get("http://" + address + "/missing")
					if err == nil {
						_, _ = io.Copy(io.Discard, response.Body)
						_ = response.Body.Close()
						break
					}
					select {
					case <-done:
						t.Fatalf("满 stdout 子进程提前退出: %v stderr=%s", waitErr, stderr.String())
					default:
					}
					if time.Now().After(deadline) {
						t.Fatal("满 stdout 子进程没有监听")
					}
					time.Sleep(5 * time.Millisecond)
				}
			}
			started := time.Now()
			if err := command.Process.Signal(syscall.SIGTERM); err != nil {
				t.Fatal(err)
			}
			select {
			case <-done:
			case <-time.After(7 * time.Second):
				t.Fatal("真实继承 stdout 写满后取消仍不能退出")
			}
			if waitErr != nil {
				t.Fatalf("stdout 子进程异常退出: %v stderr=%s", waitErr, stderr.String())
			}
			t.Logf("full=%t，SIGTERM 后 %s 退出", full, time.Since(started))
			control, err := io.ReadAll(controlRead)
			if err != nil || string(control) != "original stdout open; no original deadline\n" {
				t.Fatalf("原 stdout 所有权或期限被改变: %q %v", control, err)
			}
			if !full {
				if err := reader.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
					t.Fatal(err)
				}
				line, err := buffered.ReadBytes('\n')
				if err != nil || !bytes.Contains(line, []byte(`"phase":"shutdown"`)) {
					t.Fatalf("缺少最终统计: %s %v", line, err)
				}
			}
		})
	}
}
