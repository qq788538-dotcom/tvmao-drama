// Package reveal 在系统文件管理器里选中一个文件（「在访达/资源管理器中显示」）。
package reveal

// InFileManager 打开 path 所在的文件夹并选中它。path 必须是已存在文件的绝对路径。
func InFileManager(path string) error { return inFileManager(path) }

// windowsCmdLine 拼 explorer 的原始命令行。explorer 自己解析 /select, 参数，不认 Go 的标准转义
// （会把整个 "/select,C:\a b.png" 包进一对引号，结果选不中文件），所以只给路径本身加引号。
// 放在不带构建标签的文件里，是为了在 macOS 上也能测它。
func windowsCmdLine(path string) string {
	return `explorer.exe /select,"` + path + `"`
}
