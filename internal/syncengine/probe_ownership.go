package syncengine

import (
	"errors"
	"os"
)

func createProbe(root *os.Root, state *State, path, kind string, save func() error) error {
	var err error
	if kind == "folder" {
		err = root.Mkdir(path, 0700)
	} else {
		var f *os.File
		f, err = root.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err == nil {
			err = f.Close()
		}
	}
	if err != nil {
		return err
	}
	if err = (&executor{root: root}).parentSync(path); err != nil {
		return err
	}
	info, err := root.Lstat(path)
	if err != nil {
		return err
	}
	identity, err := fileIdentity(info)
	if err != nil {
		return err
	}
	if state.ProbeIdentities == nil {
		state.ProbeIdentities = map[string]FileIdentity{}
	}
	state.ProbeIdentities[path] = identity
	if err = save(); err != nil {
		if checkErr := checkProbe(root, state, path, kind); checkErr != nil {
			return errors.Join(err, checkErr)
		}
		if removeErr := root.Remove(path); removeErr != nil {
			return errors.Join(err, removeErr)
		}
		return err
	}
	return nil
}

func checkProbe(root *os.Root, state *State, path, kind string) error {
	info, err := root.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return errors.New("cannot inspect validation probe")
	}
	if (kind == "folder" && !info.IsDir()) || (kind == "file" && (!info.Mode().IsRegular() || info.Size() != 0)) {
		return errors.New("validation probe has unexpected content or type")
	}
	if identity, ok := state.ProbeIdentities[path]; ok {
		actual, err := fileIdentity(info)
		if err != nil || actual != identity {
			return errors.New("validation probe ownership changed")
		}
	} else if state.Version != 1 {
		return errors.New("validation probe ownership is unconfirmed")
	}
	return nil
}
