package a2aruntime

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"

	"github.com/tangxusc/block-play-table/pkg/a2aext"
)

func (r *Runtime) runShell(item *execution, environment map[string]string, command string) error {
	if strings.TrimSpace(command) == "" {
		return nil
	}
	r.emitLog(item, a2aext.LogSystem, "$ "+command, false)
	shell, args := "sh", []string{"-c", command}
	if runtime.GOOS == "windows" {
		shell, args = "cmd", []string{"/C", command}
	}
	cmd := exec.Command(shell, args...)
	cmd.Dir = item.worktreePath
	cmd.Env = mergeEnvironment(environment)
	configureProcessGroup(cmd)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	var readers sync.WaitGroup
	readers.Add(2)
	go copyLogPipe(&readers, stdout, func(chunk string) { r.emitLog(item, a2aext.LogStdout, chunk, false) })
	go copyLogPipe(&readers, stderr, func(chunk string) { r.emitLog(item, a2aext.LogStderr, chunk, false) })
	done := make(chan error, 1)
	go func() {
		readers.Wait()
		done <- cmd.Wait()
	}()
	select {
	case commandErr := <-done:
		if commandErr != nil {
			return fmt.Errorf("命令 %q 执行失败: %w", command, commandErr)
		}
		return nil
	case <-item.ctx.Done():
		killProcessGroup(cmd)
		_ = stdout.Close()
		_ = stderr.Close()
		<-done
		return context.Cause(item.ctx)
	}
}

func copyLogPipe(wait *sync.WaitGroup, reader io.Reader, emit func(string)) {
	defer wait.Done()
	buffer := make([]byte, a2aext.MaxLogChunkBytes)
	for {
		count, err := reader.Read(buffer)
		if count > 0 {
			emit(string(buffer[:count]))
		}
		if err != nil {
			return
		}
	}
}

func runProcess(ctx context.Context, dir string, environment map[string]string, name string, args ...string) ([]byte, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = mergeEnvironment(environment)
	configureProcessGroup(cmd)
	var output bytes.Buffer
	cmd.Stdout, cmd.Stderr = &output, &output
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("启动 %s: %w", name, err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			return nil, fmt.Errorf("%s %s 执行失败: %w: %s", name, strings.Join(args, " "), err, output.String())
		}
		return output.Bytes(), nil
	case <-ctx.Done():
		killProcessGroup(cmd)
		<-done
		return nil, context.Cause(ctx)
	}
}

func mergeEnvironment(extra map[string]string) []string {
	result := os.Environ()
	for key, value := range extra {
		result = append(result, key+"="+value)
	}
	return result
}
