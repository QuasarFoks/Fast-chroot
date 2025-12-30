package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"
)

var (
	userSpec       = flag.String("u", "", "user[:group] to run as")
	skipResolvConf = flag.Bool("r", false, "do not update resolv.conf")
	showHelp       = flag.Bool("h", false, "show help")
	verbose        = flag.Bool("v", false, "verbose output")
)

func main() {
	flag.Parse()

	if *showHelp {
		printHelp()
		os.Exit(0)
	}

	if flag.NArg() < 1 {
		fatalf("No chroot directory specified")
	}

	chrootDir := flag.Arg(0)
	cmdArgs := flag.Args()[1:]
	if len(cmdArgs) == 0 {
		cmdArgs = []string{"/bin/bash"}
	}

	// Проверка root
	if os.Geteuid() != 0 {
		fatalf("This program must be run as root")
	}

	// Проверка существования директории
	if _, err := os.Stat(chrootDir); os.IsNotExist(err) {
		fatalf("Chroot directory does not exist: %s", chrootDir)
	}

	// Получаем абсолютный путь
	absChrootDir, err := filepath.Abs(chrootDir)
	if err != nil {
		fatalf("Failed to get absolute path: %v", err)
	}
	chrootDir = absChrootDir

	logInfo("Using chroot directory: %s", chrootDir)

	// Обработка прерывания для корректного размонтирования
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigChan
		logInfo("Received interrupt signal, unmounting...")
		umountEssentials(chrootDir)
		os.Exit(1)
	}()

	// Проверка mountpoint перед началом
	checkMountpoint(chrootDir)

	// Монтирование (proc, sys, dev)
	mountEssentials(chrootDir)
	defer umountEssentials(chrootDir)

	// resolv.conf
	if !*skipResolvConf {
		setupResolvConf(chrootDir)
	}

	// Запуск chroot
	runChroot(chrootDir, *userSpec, cmdArgs)
}

func fatalf(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "error: "+format+"\n", args...)
	os.Exit(1)
}

func logInfo(format string, args ...interface{}) {
	if *verbose {
		fmt.Printf("info: "+format+"\n", args...)
	}
}

func printHelp() {
	fmt.Printf(`fchroot - simple chroot wrapper with auto-mounting

	Usage: fchroot [options] chroot-dir [command...]

	Options:
	-h                  Show this help
	-u <user>[:group]   Run as specified user
	-r                  Do not update resolv.conf
	-v                  Verbose output

	Examples:
	fchroot /mnt/chroot
	fchroot -u nobody /mnt/chroot /bin/sh
	fchroot -v /mnt/chroot /bin/bash -l

	Default command: /bin/bash
	`)
}

func mountEssentials(chrootDir string) {
	mounts := []struct {
		name   string
		source string
		fstype string
	}{
		{"proc", "/proc", "proc"},
		{"sys", "/sys", "sysfs"},
		{"dev", "/dev", ""}, // bind mount
	}

	logInfo("Mounting essential filesystems...")

	for _, mnt := range mounts {
		target := filepath.Join(chrootDir, mnt.name)

		// Создаём директорию, если её нет
		if err := os.MkdirAll(target, 0755); err != nil {
			fatalf("Failed to create directory %s: %v", target, err)
		}

		// Проверяем, не смонтировано ли уже
		if isMounted(target) {
			logInfo("%s is already mounted, skipping", target)
			continue
		}

		args := []string{}
		if mnt.fstype == "" {
			// bind mount
			args = []string{"--bind", mnt.source, target}
			logInfo("Mounting %s (bind) -> %s", mnt.source, target)
		} else {
			// regular mount
			args = []string{"-t", mnt.fstype, mnt.source, target}
			logInfo("Mounting %s (%s) -> %s", mnt.source, mnt.fstype, target)
		}

		cmd := exec.Command("mount", args...)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr

		if err := cmd.Run(); err != nil {
			fatalf("Failed to mount %s -> %s: %v", mnt.source, target, err)
		}
		fmt.Printf("✓ Mounted %s\n", mnt.name)
	}
}

func setupResolvConf(chrootDir string) {
	hostResolv := "/etc/resolv.conf"
	chrootResolv := filepath.Join(chrootDir, "etc/resolv.conf")
	chrootEtcDir := filepath.Join(chrootDir, "etc")

	logInfo("Setting up resolv.conf...")

	// Создаём директорию /etc внутри chroot, если её нет
	if err := os.MkdirAll(chrootEtcDir, 0755); err != nil {
		fatalf("Failed to create /etc in chroot: %v", err)
	}

	// Пытаемся создать симлинк
	err := os.Symlink(hostResolv, chrootResolv)
	if err == nil {
		fmt.Printf("✓ resolv.conf: symlinked %s → %s\n", hostResolv, chrootResolv)
		return
	}

	// Если симлинк не получился — удаляем старый файл (если есть)
	if _, statErr := os.Stat(chrootResolv); statErr == nil {
		if err := os.Remove(chrootResolv); err != nil {
			fatalf("Failed to remove existing %s: %v", chrootResolv, err)
		}
		logInfo("Removed existing %s", chrootResolv)
	}

	// Теперь копируем
	fmt.Printf("→ resolv.conf: copying %s → %s\n", hostResolv, chrootResolv)

	src, err := os.Open(hostResolv)
	if err != nil {
		fatalf("Failed to open %s: %v", hostResolv, err)
	}
	defer src.Close()

	dst, err := os.Create(chrootResolv)
	if err != nil {
		fatalf("Failed to create %s: %v", chrootResolv, err)
	}
	defer dst.Close()

	_, err = io.Copy(dst, src)
	if err != nil {
		fatalf("Failed to copy resolv.conf: %v", err)
	}

	fmt.Printf("✓ resolv.conf: copied %s → %s\n", hostResolv, chrootResolv)
}

func checkMountpoint(chrootDir string) {
	// Проверяем, смонтирован ли chrootDir
	if !isMounted(chrootDir) {
		fmt.Printf("⚠  Warning: %s is not a mountpoint\n", chrootDir)
		fmt.Printf("   This might be intentional if you're using directory chroot\n")
	} else {
		logInfo("Chroot directory %s is a mountpoint", chrootDir)
	}
}

func isMounted(path string) bool {
	cmd := exec.Command("findmnt", "-n", "-o", "TARGET", "--target", path)
	output, err := cmd.Output()
	if err != nil {
		return false
	}
	return len(output) > 0
}

func runChroot(chrootDir string, userSpec string, cmdArgs []string) {
	// Собираем аргументы для chroot
	args := []string{}
	if userSpec != "" {
		args = append(args, "--userspec", userSpec)
	}
	args = append(args, chrootDir)
	args = append(args, cmdArgs...)

	fmt.Printf("→ Executing: chroot %v\n", args)

	// Запускаем chroot и передаём управление
	cmd := exec.Command("chroot", args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Pdeathsig: syscall.SIGTERM,
	}

	err := cmd.Run()
	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		}
		fmt.Printf("✗ chroot exited with code %d\n", exitCode)
		os.Exit(exitCode)
	}
	fmt.Printf("✓ chroot completed successfully\n")
}

func umountEssentials(chrootDir string) {
	// Размонтируем в обратном порядке!
	mounts := []string{"dev", "sys", "proc"}

	fmt.Println("→ Unmounting filesystems...")

	for _, fs := range mounts {
		target := filepath.Join(chrootDir, fs)

		// Проверяем, смонтирован ли вообще
		if !isMounted(target) {
			logInfo("%s is not mounted, skipping", target)
			continue
		}

		logInfo("Unmounting %s", target)

		// Пытаемся размонтировать несколько раз с задержкой
		var lastErr error
		for attempt := 1; attempt <= 3; attempt++ {
			cmd := exec.Command("umount", target)
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr

			if err := cmd.Run(); err != nil {
				lastErr = err
				if attempt < 3 {
					logInfo("Attempt %d failed, retrying in 1s...", attempt)
					time.Sleep(1 * time.Second)
				}
				continue
			}

			fmt.Printf("✓ Unmounted %s\n", target)
			lastErr = nil
			break
		}

		if lastErr != nil {
			fmt.Fprintf(os.Stderr, "✗ Failed to unmount %s after 3 attempts: %v\n", target, lastErr)
			fmt.Fprintf(os.Stderr, "   You may need to unmount it manually: umount %s\n", target)
		}
	}

	// Также пытаемся размонтировать симлинк resolv.conf если он был создан
	resolvPath := filepath.Join(chrootDir, "etc/resolv.conf")
	if fi, err := os.Lstat(resolvPath); err == nil {
		if fi.Mode()&os.ModeSymlink != 0 {
			// Это симлинк, можно удалить
			if err := os.Remove(resolvPath); err == nil {
				logInfo("Removed resolv.conf symlink")
			}
		}
	}

	fmt.Println("→ Cleanup completed")
}
