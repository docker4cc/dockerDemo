package container

import (
	"fmt"
	log "github.com/sirupsen/logrus"
	"os"
	"os/exec"
	"strings"
	"syscall"
)

// NewParentProcess
/*
这里是父进程，也就是当前进程执行的内容
1.这里的 /proc/self/exe 调用中， /proc/self/ 指定是当前运行进程自己的环境，exec是自己调用自己，使用这种方式对创造出来的进程进行初始化
2.后面的args是参数，其中init是传递给本进程的第一个参数，在本例中，其实就是会去调用initCommand去初始化进程的一些环境和资源
3.下面的clone参数就是去fork出来一个新进程，并且使用了namespace隔离新创建的进程和外部环境
4.如果用户指定了-ti参数，就需要把当前进程的输入输出导入到标准输入输出上
*/
func NewParentProcess(tty bool, containerName, rootUrl, mntUrl, volume string, envSlice []string) (*exec.Cmd, *os.File) {
	readPipe, writePipe, err := os.Pipe()
	if err != nil {
		log.Errorf("create pipe error: %v", err)
		return nil, nil
	}

	cmd := exec.Command("/proc/self/exe", "init")
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Cloneflags: syscall.CLONE_NEWUTS | syscall.CLONE_NEWPID | syscall.CLONE_NEWNS | syscall.CLONE_NEWNET | syscall.CLONE_NEWIPC,
	}
	if tty {
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
	} else {
		dirUrl := fmt.Sprintf(DefaultInfoLocation, containerName)
		if err := os.MkdirAll(dirUrl, 0622); err != nil {
			log.Errorf("mkdir dir %s, err: %v", dirUrl, err)
			return nil, nil
		}

		stdLogFilePath := dirUrl + ContainerLogFile
		stdLogFile, err := os.Create(stdLogFilePath)
		if err != nil {
			log.Errorf("create file %s, err: %v", stdLogFilePath, err)
			return nil, nil
		}

		cmd.Stdout = stdLogFile
	}

	// 将管道的一端传入fork的进程中
	cmd.ExtraFiles = []*os.File{readPipe}
	if err := newWorkSpace(rootUrl, mntUrl, volume, containerName); err != nil {
		log.Errorf("new work space err: %v", err)
		return nil, nil
	}
	cmd.Dir = mntUrl
	cmd.Env = append(os.Environ(), envSlice...)
	return cmd, writePipe
}

func newWorkSpace(rootUrl, mntUrl, volume, containerName string) error {
	if err := createReadOnlyLayer(rootUrl); err != nil {
		return err
	}
	if err := createWriteLayer(containerName); err != nil {
		return err
	}
	if err := createMountPoint(rootUrl, mntUrl, containerName); err != nil {
		return err
	}
	if err := mountExtractVolume(mntUrl, volume, containerName); err != nil {
		return err
	}
	return nil
}

// 我们直接把busybox放到了工程目录下，直接作为容器的只读层
func createReadOnlyLayer(busyboxUrl string) error {
	exist, err := pathExist(busyboxUrl)
	if err != nil {
		return err
	}
	if !exist {
		return fmt.Errorf("busybox dir don't exist: %s", busyboxUrl)
	}
	return nil
}

// 创建一个名为writeLayer的文件夹作为容器的唯一可写层
func createWriteLayer(containerName string) error {
	writeUrl := RootUrl + "/writeLayer/" + containerName + "/"
	exist, err := pathExist(writeUrl)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if !exist {
		if err := os.MkdirAll(writeUrl, 0777); err != nil {
			return fmt.Errorf("create write layer failed: %v", err)
		}
	}
	workUrl := RootUrl + "/work/" + containerName + "/"
	exist, err = pathExist(workUrl)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if !exist {
		if err := os.MkdirAll(workUrl, 0777); err != nil {
			return fmt.Errorf("create work dir failed: %v", err)
		}
	}
	return nil
}

func createMountPoint(rootUrl, mntUrl, containerName string) error {
	// 创建mnt文件夹作为挂载点
	mountPath := mntUrl + containerName + "/"
	log.Infof("root url: %s, mntUrl: %s, mountPath: %s", rootUrl, mntUrl, mountPath)
	exist, err := pathExist(mountPath)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if !exist {
		if err := os.MkdirAll(mountPath, 0777); err != nil {
			return fmt.Errorf("mkdir faild: %v", err)
		}
	}
	// 把writeLayer和busybox目录mount到mnt目录下
	// 使用 OverlayFS 代替 AUFS
	lowerdir := rootUrl
	upperdir := RootUrl + "/writeLayer/" + containerName
	workdir := RootUrl + "/work/" + containerName

	if err := os.MkdirAll(workdir, 0777); err != nil {
		return fmt.Errorf("mkdir workdir failed: %v", err)
	}

	dirs := fmt.Sprintf("lowerdir=%s,upperdir=%s,workdir=%s", lowerdir, upperdir, workdir)
	cmd := exec.Command("mount", "-t", "overlay", "overlay", "-o", dirs, mountPath)
	
	
	// dirs := "dirs=" + RootUrl + "/writeLayer:" + rootUrl
	// cmd := exec.Command("mount", "-t", "aufs", "-o", dirs, "none", mountPath)
	log.Infof(cmd.String())
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("mmt dir err: %v", err)
	}
	return nil
}

func mountExtractVolume(mntUrl, volume, containerName string) error {
	if volume == "" {
		return nil
	}
	volumeUrls := strings.Split(volume, ":")
	length := len(volumeUrls)
	if length != 2 || volumeUrls[0] == "" || volumeUrls[1] == "" {
		return fmt.Errorf("volume parameter input is not corrent")
	}
	return mountVolume(mntUrl+containerName+"/", volumeUrls)
}

func mountVolume(mntUrl string, volumeUrls []string) error {
	// 如果宿主机文件目录不存在则创建
	parentUrl := volumeUrls[0]
	exist, err := pathExist(parentUrl)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if !exist {
		// 使用mkdir all 递归创建文件夹
		if err := os.MkdirAll(parentUrl, 0777); err != nil {
			return fmt.Errorf("mkdir parent dir err: %v", err)
		}
	}

	// 在容器文件系统内创建挂载点
	containerUrl := mntUrl + volumeUrls[1]
	if err := os.MkdirAll(containerUrl, 0777); err != nil {
		return fmt.Errorf("mkdir container volume err: %v", err)
	}

	// 把宿主机文件目录挂载到容器挂载点
	dirs := "dirs=" + parentUrl
	cmd := exec.Command("mount", "-t", "aufs", "-o", dirs, "none", containerUrl)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("mount volume err: %v", err)
	}
	return nil
}

func pathExist(path string) (bool, error) {
	_, err := os.Stat(path)
	if err == nil {
		return true, err
	}
	return false, err
}
