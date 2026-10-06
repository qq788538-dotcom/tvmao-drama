package datadir

// SetTrace 让外部测试记录持久化调用顺序；返回恢复函数。
func SetTrace(f func(op, path string)) (restore func()) {
	old := trace
	trace = f
	return func() { trace = old }
}
