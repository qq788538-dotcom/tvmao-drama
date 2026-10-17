package http

// presetMemSettings 是 settings.Repository 的内存实现，供预设 / 多角度 / 打光的测试用。
// 起这个名字是为了避免与其他测试替身重名。
type presetMemSettings struct{ kv map[string]string }

func newPresetMemSettings() *presetMemSettings { return &presetMemSettings{kv: map[string]string{}} }

func (s *presetMemSettings) Get(k string) (string, error)    { return s.kv[k], nil }
func (s *presetMemSettings) Set(k, v string) error           { s.kv[k] = v; return nil }
func (s *presetMemSettings) All() (map[string]string, error) { return s.kv, nil }
