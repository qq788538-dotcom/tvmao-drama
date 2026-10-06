package reveal

import "os/exec"

func inFileManager(path string) error {
	return exec.Command("open", "-R", path).Run()
}
