// Package bootstrapenv 负责把「首次登入引导」的新凭据原子地回写进 env 文件。
//
// 设计约束（用户明确要求）：
//   - 只替换目标键，其它行原样保留；
//   - 原子写入（同目录临时文件 + rename），不会出现半截文件；
//   - 文件权限保持 0600；
//   - 任何值都不写日志（本包只做键值替换，不打印内容）。
package bootstrapenv

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ErrEmptyPath 表示未配置 env 文件路径（调用方应按「未启用回写」处理，而非失败）。
var ErrEmptyPath = errors.New("bootstrap env file path is empty")

// Update 把 updates 中的键写回 path：
//   - 文件中已存在的键 → 就地替换该行的值；
//   - 文件中不存在的键 → 追加到末尾；
//   - 其它行（含注释、空行、无关键）保持原样。
//
// 写入过程为「同目录临时文件 + fsync + rename」，最终文件权限固定 0600。
func Update(path string, updates map[string]string) error {
	if strings.TrimSpace(path) == "" {
		return ErrEmptyPath
	}
	if len(updates) == 0 {
		return nil
	}

	original, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read bootstrap env file: %w", err)
	}

	lines := splitLines(string(original))
	remaining := make(map[string]string, len(updates))
	for key, value := range updates {
		remaining[key] = value
	}

	for index, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		separator := strings.Index(line, "=")
		if separator < 0 {
			continue
		}
		key := strings.TrimSpace(line[:separator])
		value, ok := remaining[key]
		if !ok {
			continue
		}
		// 保留键名与排版，只替换等号后面的值。
		lines[index] = key + "=" + value
		delete(remaining, key)
	}

	// 追加缺失的键，用排序保证输出确定（便于测试与 diff）。
	missingKeys := make([]string, 0, len(remaining))
	for key := range remaining {
		missingKeys = append(missingKeys, key)
	}
	sort.Strings(missingKeys)
	for _, key := range missingKeys {
		lines = append(lines, key+"="+remaining[key])
	}

	content := strings.Join(lines, "\n")
	if content != "" {
		content += "\n"
	}
	return writeAtomic(path, []byte(content))
}

// splitLines 把文件拆成行，去掉末尾换行，随后统一以 "\n" 重组。
func splitLines(content string) []string {
	if content == "" {
		return nil
	}
	trimmed := strings.TrimSuffix(content, "\n")
	return strings.Split(trimmed, "\n")
}

func writeAtomic(path string, content []byte) error {
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("ensure bootstrap env directory: %w", err)
	}
	tempFile, err := os.CreateTemp(directory, ".neilico-bootstrap-*")
	if err != nil {
		return fmt.Errorf("create temp bootstrap env file: %w", err)
	}
	tempName := tempFile.Name()
	cleanup := func() {
		if tempName != "" {
			_ = os.Remove(tempName)
		}
	}
	if err := tempFile.Chmod(0o600); err != nil {
		_ = tempFile.Close()
		cleanup()
		return fmt.Errorf("chmod temp bootstrap env file: %w", err)
	}
	if _, err := tempFile.Write(content); err != nil {
		_ = tempFile.Close()
		cleanup()
		return fmt.Errorf("write temp bootstrap env file: %w", err)
	}
	if err := tempFile.Sync(); err != nil {
		_ = tempFile.Close()
		cleanup()
		return fmt.Errorf("sync temp bootstrap env file: %w", err)
	}
	if err := tempFile.Close(); err != nil {
		cleanup()
		return fmt.Errorf("close temp bootstrap env file: %w", err)
	}
	if err := os.Rename(tempName, path); err != nil {
		// 目标可能是【单文件 bind mount】（docker -v <宿主文件>:<容器文件>）：
		// rename 到挂载点必然 EBUSY（实测：device or resource busy）。
		// 退化为「就地重写」——保持目标文件 inode/属主/mode 不变，只替换内容。
		content, readErr := os.ReadFile(tempName)
		if readErr != nil {
			cleanup()
			return fmt.Errorf("replace bootstrap env file: %w", err)
		}
		target, openErr := os.OpenFile(path, os.O_WRONLY|os.O_TRUNC, 0o600)
		if openErr != nil {
			cleanup()
			return fmt.Errorf("replace bootstrap env file: %w (in-place open: %v)", err, openErr)
		}
		_, writeErr := target.Write(content)
		syncErr := target.Sync()
		closeErr := target.Close()
		cleanup()
		if writeErr != nil || syncErr != nil || closeErr != nil {
			return fmt.Errorf("replace bootstrap env file: %w (in-place write: %v/%v/%v)", err, writeErr, syncErr, closeErr)
		}
		return nil
	}
	// rename 成功后临时文件已不存在，避免误删目标。
	tempName = ""
	return nil
}
