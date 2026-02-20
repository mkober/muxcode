package bus

import "os"

// Cleanup removes the bus directory and trigger file for a session.
func Cleanup(session string) error {
	if err := os.RemoveAll(BusDir(session)); err != nil {
		return err
	}
	err := os.Remove(TriggerFile(session))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}
