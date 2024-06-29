package container

import (
	"fmt"
	log "github.com/sirupsen/logrus"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

// RunContainerInitProcess
/*
之类的init函数是在容器内部执行的，也就是说，代码执行到这里后，容器所在的进程其实就已经创建出来了，这是本容器执行的第一个进程。
使用mount先去挂载proc文件系统，以便于后面通过ps命等系统命令去查看当前进程资源的情况
*/
func RunContainerInitProcess() error {
	if err := setUpMount(); err != nil {
		return err
	}

	cmdArray := readUserCommand()
	log.Infof("cmd Array []: %s", cmdArray[0])
	path, err := exec.LookPath(cmdArray[0])
	if err != nil {
		log.Errorf("can't find exec path: %s %v", cmdArray[0], err)
		return err
	}
	log.Infof("find path: %s", path)
	if err := syscall.Exec(path, cmdArray, os.Environ()); err != nil {
		log.Errorf("syscall exec err: %v", err.Error())
	}
	return nil
}

// 读取程序传入参数
func readUserCommand() []string {
	// 进程默认三个管道，从fork那边传过来的就是第四个（从0开始计数）
	readPipe := os.NewFile(uintptr(3), "pipe")
	msg, err := ioutil.ReadAll(readPipe)
	if err != nil {
		log.Errorf("read init argv pipe err: %v", err)
		return nil
	}
	return strings.Split(string(msg), " ")
}

// 初始化挂载点
func setUpMount() error {
    // 设置根目录为私有模式，防止影响 pivot_root
    if err := syscall.Mount("/", "/", "", syscall.MS_REC|syscall.MS_PRIVATE, ""); err != nil {
        return fmt.Errorf("setUpMount Mount proc err: %v", err)
    }
	// 打印
    // 进入 busybox, 固定路径，busybox 提前解压好，放到指定的配置路径
    // err := privotRoot(RootUrl + "/mnt/busybox")
    
    // 打印 Busybox 应该存在的绝对路径
	absoluteBusyboxPath, err := filepath.Abs(BusyboxPath)
	if err != nil {
		return fmt.Errorf("failed to get absolute path of BusyboxPath: %v", err)
	}
	log.Infof("busybox absolute path: %s", absoluteBusyboxPath)
    // 检查 busybox 是否存在
    if _, err := os.Stat(BusyboxPath); err != nil {
        return fmt.Errorf("busybox path does not exist: %v", err)
    }

    err = privotRoot(BusyboxPath)
    if err != nil {
        return err
    }

    // 挂载 proc 文件系统
    syscall.Unmount("/proc", 0)
    defaultMountFlags := syscall.MS_NOEXEC | syscall.MS_NOSUID | syscall.MS_NODEV
    err = syscall.Mount("proc", "/proc", "proc", uintptr(defaultMountFlags), "")
    if err != nil {
        log.Errorf("proc 挂载失败: %v", err)
        return err
    }

    // 挂载 tmpfs 到 /dev 目录
    syscall.Mount("tmpfs", "/dev", "tmpfs", syscall.MS_NOSUID|syscall.MS_STRICTATIME, "mode=755")
    return nil
}


func privotRoot(root string) error {
	pwd, err := os.Getwd()
	if err != nil {
		log.Errorf("pwd err: %v", err)
		return err
	}
	log.Infof("current pwd: %s", pwd)
	log.Infof("privotRoot root: %s", root)  // 添加日志

	// 确保 root 是绝对路径
	root, err = filepath.Abs(root)
	if err != nil {
		return fmt.Errorf("failed to get absolute path of root: %v", err)
	}

	// 使用 OverlayFS 挂载文件系统
	upperdir := filepath.Join(root, "upper")
	workdir := filepath.Join(root, "work")
	lowerdir := filepath.Join(root, "lower")

	log.Infof("creating upperdir: %s", upperdir)
	if err := os.MkdirAll(upperdir, 0777); err != nil {
		return fmt.Errorf("mkdir upperdir err: %v", err)
	}
	log.Infof("creating workdir: %s", workdir)
	if err := os.MkdirAll(workdir, 0777); err != nil {
		return fmt.Errorf("mkdir workdir err: %v", err)
	}
	log.Infof("creating lowerdir: %s", lowerdir)
	if err := os.MkdirAll(lowerdir, 0777); err != nil {
		return fmt.Errorf("mkdir lowerdir err: %v", err)
	}

	// 创建 rootfs/.pivot_root 用于临时存储 old_root
	pivotDir := filepath.Join(root, ".pivot_root")
	log.Infof("pivotDir: %s", pivotDir)

	// 确保 pivotDir 的父目录存在
	parentDir := filepath.Dir(pivotDir)
	if err := os.MkdirAll(parentDir, 0777); err != nil {
		return fmt.Errorf("mkdir parent dir of pivot_root err: %v", err)
	}

	// 如果 pivotDir 已经存在，先删除
	if _, err := os.Stat(pivotDir); err == nil {
		if err := os.Remove(pivotDir); err != nil {
			return err
		}
	}

	// 创建 pivotDir
	log.Infof("creating pivotDir: %s", pivotDir)
	if err := os.Mkdir(pivotDir, 0777); err != nil {
		return fmt.Errorf("mkdir of pivot_root err: %v", err)
	}
	log.Infof("pivotDir created: %s", pivotDir)

	// 确保 pivotDir 已经存在
	if _, err := os.Stat(pivotDir); err != nil {
		absolutePivotDir, _ := filepath.Abs(pivotDir)
		log.Errorf("pivotDir does not exist after creation, absolute path: %s", absolutePivotDir)
		return fmt.Errorf("pivotDir does not exist after creation: %v", err)
	}

	options := fmt.Sprintf("lowerdir=%s,upperdir=%s,workdir=%s", lowerdir, upperdir, workdir)
	if err := syscall.Mount("overlay", root, "overlay", 0, options); err != nil {
		return fmt.Errorf("mount overlay err: %v", err)
	}
    // 挂载后再创建一次 pivotDir，确保在新的文件系统视图中存在
	log.Infof("re-creating pivotDir after mounting overlay: %s", pivotDir)
	if err := os.Mkdir(pivotDir, 0777); err != nil {
		return fmt.Errorf("re-create pivot_root err: %v", err)
	}

	// 再次确保 pivotDir 存在
	if _, err := os.Stat(pivotDir); err != nil {
		absolutePivotDir, _ := filepath.Abs(pivotDir)
		log.Errorf("pivotDir does not exist after mounting overlay, absolute path: %s", absolutePivotDir)
		return fmt.Errorf("pivotDir does not exist after mounting overlay: %v", err)
	}

	// pivot_root 到新的 rootfs，老的 old_root 现在挂载在 rootfs/.pivot_root 上
	log.Infof("root: %s， pivotDir: %s", root, pivotDir)
	if err := syscall.PivotRoot(root, pivotDir); err != nil {
		log.Errorf("root path: %s, pivotDir path: %s", root, pivotDir)
		return fmt.Errorf("pivot_root err: %v", err)
	}

	// 修改当前工作目录到根目录
	if err := syscall.Chdir("/"); err != nil {
		return fmt.Errorf("chdir root err: %v", err)
	}

	// 取消临时文件 .pivot_root 的挂载并删除它
	pivotDir = filepath.Join("/", ".pivot_root")
	if err := syscall.Unmount(pivotDir, syscall.MNT_DETACH); err != nil {
		return fmt.Errorf("unmount pivot_root dir err: %v", err)
	}
	return os.Remove(pivotDir)
}

