package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
)

var (
	userSpec       = flag.String("u", "", "user[:group] to run as")
	skipResolvConf = flag.Bool("r", false, "do not update resolv.conf")
	showHelp       = flag.Bool("h", false, "show help")
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

	// Монтирование (proc, sys, dev)
	mountEssentials(chrootDir)
	defer umountEssentials(chrootDir)

	// resolv.conf
	if !*skipResolvConf {
		setupResolvConf(chrootDir)
	}

	// Проверка mountpoint — warning, не ошибка
	checkMountpoint(chrootDir)

	// Запуск chroot
	runChroot(chrootDir, *userSpec, cmdArgs)
}

func fatalf(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "error: "+format+"\n", args...)
	os.Exit(1)
}

func printHelp() {
	fmt.Printf(`usage: fast-chroot chroot-dir [command...]

    -h                  Show this help
    -u <user>[:group]   Run as specified user
    -r                  Do not update resolv.conf

If command is unspecified, runs /bin/bash.
`)
}
//	<<<<<<<<<<<<<
// 	< mount fs  <
//	<<<<<<<<<<<<<
func mountEssentials(chrootDir string) {
	mounts := []string{"proc", "sys", "dev"}

	for _, fs := range mounts {
		source := "/" + fs
		target := chrootDir + "/" + fs

		// Создаём директорию, если её нет
		if err := os.MkdirAll(target, 0755); err != nil {
			fatalf("Failed to create directory %s: %v", target, err)
		}

		// Выполняем mount --bind
		cmd := exec.Command("mount", "--bind", source, target)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr

		if err := cmd.Run(); err != nil {
			fatalf("Failed to bind mount %s -> %s: %v", source, target, err)
		}
	}
}

func setupResolvConf(chrootDir string) {
	hostResolv := "/etc/resolv.conf"
	chrootResolv := chrootDir + "/etc/resolv.conf"

	// Создаём директорию /etc внутри chroot, если её нет
	if err := os.MkdirAll(chrootDir+"/etc", 0755); err != nil {
		fatalf("Failed to create /etc in chroot: %v", err)
	}

	// Пытаемся создать симлинк
	err := os.Symlink(hostResolv, chrootResolv)
	if err == nil {
		fmt.Printf("→ resolv.conf: symlinked %s → %s\n", hostResolv, chrootResolv)
		return
	}

	// Если симлинк не получился — удаляем старый файл (если есть)
	if _, statErr := os.Stat(chrootResolv); statErr == nil {
		if err := os.Remove(chrootResolv); err != nil {
			fatalf("Failed to remove existing %s: %v", chrootResolv, err)
		}
		fmt.Printf("→ removed existing %s\n", chrootResolv)
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

	fmt.Printf("→ resolv.conf: copied %s → %s\n", hostResolv, chrootResolv)
}
func checkMountpoint(chrootDir string) {
	// Пока просто предупреждение — без реальной проверки
	fmt.Printf(" Warning: %s is not checked as mountpoint (not implemented yet)\n", chrootDir)
}
func runChroot(chrootDir string, userSpec string, cmdArgs []string) {
	// Собираем аргументы для chroot
	args := []string{chrootDir}
	if userSpec != "" {
		args = append([]string{"--userspec", userSpec}, args...)
	}
	args = append(args, cmdArgs...)

	fmt.Printf("→ Executing: chroot %v\n", args)

	// Запускаем chroot и передаём управление
	cmd := exec.Command("chroot", args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	err := cmd.Run()
	if err != nil {
		fatalf("chroot failed: %v", err)
	}
}
func umountEssentials(chrootDir string) {
	// Размонтируем в обратном порядке!
	mounts := []string{"dev", "sys", "proc"}

	for _, fs := range mounts {
		target := chrootDir + "/" + fs

		cmd := exec.Command("umount", target)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr

		if err := cmd.Run(); err != nil {
			fmt.Fprintf(os.Stderr, " Failed to unmount %s: %v\n", target, err)
			// Не падаем — просто предупреждаем
		} else {
			fmt.Printf("→ Unmounted %s\n", target)
		}
	}
}

