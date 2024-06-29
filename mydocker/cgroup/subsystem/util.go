package subsystem

import (
	"bufio"
	"fmt"
	log "github.com/sirupsen/logrus"
	"os"
	"path"
	"strings"
)

// CheckCgroupVersion 检查系统使用的是 cgroup v1 还是 cgroup v2
func CheckCgroupVersion() (string, error) {
	f, err := os.Open("/proc/filesystems")
	if err != nil {
		return "", fmt.Errorf("open /proc/filesystems err: %v", err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		txt := scanner.Text()
		if strings.Contains(txt, "cgroup2") {
			return "cgroup2", nil
		}
	}
	return "cgroup", nil
}

func FindCgroupMountPoint(subSystem string) (string, error) {
	cgroupVersion, err := CheckCgroupVersion()
	if err != nil {
		return "", err
	}
	log.Infof("cgroup version: %s", cgroupVersion)
	if cgroupVersion == "cgroup2" {
		// cgroup v2 使用统一的挂载点
		// 打印cgroup v2的挂载点：mount | grep cgroup2
		log.Infof("cgroup v2 mount point: /sys/fs/cgroup")
		return "/sys/fs/cgroup", nil
	}

	// cgroup v1 查找挂载点
	f, err := os.Open("/proc/self/mountinfo")
	if err != nil {
		return "", fmt.Errorf("open /proc/self/mountinfo err: %v", err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		txt := scanner.Text()
		fields := strings.Split(txt, " ")
		log.Debugf("mount info txt fields: %s", fields)
		for _, opt := range strings.Split(fields[len(fields)-1], ",") {
			if opt == subSystem {
				return fields[4], nil
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("file scanner err: %v", err)
	}
	return "", fmt.Errorf("FindCgroupMountPoint is empty")
}

func GetCgroupPath(subsystemName, cgroupName string) (string, error) {
	// 找到Cgroup的根目录，如：/sys/fs/cgroup/memory
	cgroupRoot, err := FindCgroupMountPoint(subsystemName)
	if err != nil {
		return "", err
	}

	cgroupPath := path.Join(cgroupRoot, cgroupName)
	_, err = os.Stat(cgroupPath)
	if err != nil && !os.IsNotExist(err) {
		return "", fmt.Errorf("file stat err: %v", err)
	}
	if os.IsNotExist(err) {
		if err := os.Mkdir(cgroupPath, os.ModePerm); err != nil {
			return "", fmt.Errorf("mkdir err: %v", err)
		}
	}
	return cgroupPath, nil
}
