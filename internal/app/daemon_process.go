package app

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"ai-sched-cli/internal/config"
)

const (
	daemonPIDFileEnv = "AI_SCHED_DAEMON_PIDFILE"
	daemonLogFileEnv = "AI_SCHED_DAEMON_LOGFILE"
)

type daemonPaths struct {
	ConfigPath string
	PIDFile    string
	LogFile    string
}

func ensureDaemon(globalConfigPath string) error {
	paths, err := resolveDaemonPaths(globalConfigPath)
	if err != nil {
		return err
	}

	pid, running, err := readDaemonPID(paths.PIDFile)
	if err != nil {
		return err
	}
	if running {
		fmt.Printf("daemon already running (pid %d)\n", pid)
		fmt.Printf("pid file: %s\n", paths.PIDFile)
		fmt.Printf("log file: %s\n", paths.LogFile)
		return nil
	}

	if err := os.MkdirAll(filepath.Dir(paths.PIDFile), 0o755); err != nil {
		return fmt.Errorf("create daemon state dir: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(paths.LogFile), 0o755); err != nil {
		return fmt.Errorf("create daemon log dir: %w", err)
	}

	logFile, err := os.OpenFile(paths.LogFile, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open daemon log file: %w", err)
	}
	defer logFile.Close()

	devNull, err := os.OpenFile(os.DevNull, os.O_RDONLY, 0)
	if err != nil {
		return fmt.Errorf("open devnull: %w", err)
	}
	defer devNull.Close()

	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve executable: %w", err)
	}

	childArgs := []string{"--config", paths.ConfigPath, "daemon"}
	cmd := exec.Command(executable, childArgs...)
	cmd.Stdin = devNull
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Env = append(os.Environ(),
		daemonPIDFileEnv+"="+paths.PIDFile,
		daemonLogFileEnv+"="+paths.LogFile,
	)

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start daemon process: %w", err)
	}
	pid = cmd.Process.Pid
	if err := cmd.Process.Release(); err != nil {
		return fmt.Errorf("detach daemon process: %w", err)
	}

	fmt.Printf("daemon started (pid %d)\n", pid)
	fmt.Printf("pid file: %s\n", paths.PIDFile)
	fmt.Printf("log file: %s\n", paths.LogFile)
	return nil
}

func printDaemonStatus(globalConfigPath string) error {
	paths, err := resolveDaemonPaths(globalConfigPath)
	if err != nil {
		return err
	}

	pid, running, err := readDaemonPID(paths.PIDFile)
	if err != nil {
		return err
	}
	if running {
		fmt.Printf("daemon running (pid %d)\n", pid)
	} else {
		fmt.Println("daemon not running")
	}
	fmt.Printf("pid file: %s\n", paths.PIDFile)
	fmt.Printf("log file: %s\n", paths.LogFile)
	return nil
}

func activateDaemonProcessGuard() (func(), error) {
	pidFile := strings.TrimSpace(os.Getenv(daemonPIDFileEnv))
	if pidFile == "" {
		return func() {}, nil
	}
	if err := os.MkdirAll(filepath.Dir(pidFile), 0o755); err != nil {
		return nil, fmt.Errorf("create daemon pid dir: %w", err)
	}
	if err := os.WriteFile(pidFile, []byte(strconv.Itoa(os.Getpid())+"\n"), 0o644); err != nil {
		return nil, fmt.Errorf("write daemon pid file: %w", err)
	}

	return func() {
		_ = os.Remove(pidFile)
	}, nil
}

func resolveDaemonPaths(globalConfigPath string) (daemonPaths, error) {
	configPath, err := resolveConfigPath(globalConfigPath)
	if err != nil {
		return daemonPaths{}, err
	}
	configPath, err = config.ExpandPath(configPath)
	if err != nil {
		return daemonPaths{}, err
	}

	baseDir := filepath.Dir(configPath)
	return daemonPaths{
		ConfigPath: configPath,
		PIDFile:    filepath.Join(baseDir, "daemon.pid"),
		LogFile:    filepath.Join(baseDir, "daemon.log"),
	}, nil
}

func readDaemonPID(pidFile string) (int, bool, error) {
	data, err := os.ReadFile(pidFile)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, false, nil
		}
		return 0, false, fmt.Errorf("read daemon pid file: %w", err)
	}

	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		_ = os.Remove(pidFile)
		return 0, false, nil
	}

	running, err := isProcessAlive(pid)
	if err != nil {
		return 0, false, err
	}
	if !running {
		_ = os.Remove(pidFile)
		return 0, false, nil
	}
	return pid, true, nil
}

func isProcessAlive(pid int) (bool, error) {
	if pid <= 0 {
		return false, nil
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		return false, fmt.Errorf("find process %d: %w", pid, err)
	}
	if err := process.Signal(syscall.Signal(0)); err != nil {
		if err == os.ErrProcessDone {
			return false, nil
		}
		if errno, ok := err.(syscall.Errno); ok {
			if errno == syscall.ESRCH {
				return false, nil
			}
			if errno == syscall.EPERM {
				return true, nil
			}
		}
		return false, fmt.Errorf("check process %d: %w", pid, err)
	}
	return true, nil
}
