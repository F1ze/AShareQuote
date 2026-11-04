package domain

type Entry struct {
	Name      string `json:"name"`      // 当前目录下的显示名称，不包含父目录。
	Path      string `json:"path"`      // 可供后续选择或浏览的完整路径。
	Directory bool   `json:"directory"` // true 为目录；gbbq 选择模式还允许返回同名因子文件。
}

type Location struct {
	Name string `json:"name"` // 快捷入口显示名，如“默认输出”“用户目录”“项目目录”。
	Path string `json:"path"` // 已存在目录的绝对路径；快捷入口不是访问范围的白名单。
}

type BrowseResult struct {
	Path    string     `json:"path"`             // 实际浏览到的绝对目录；请求目录不存在时可能回退到最近的已存在祖先。
	Parent  string     `json:"parent,omitempty"` // 可上移的父目录，文件系统根目录时为空。
	Entries []Entry    `json:"entries"`          // 目录优先、名称忽略大小写排序后的至多 500 项，按 kind 过滤文件。
	Roots   []Location `json:"roots"`            // 默认输出、用户目录、项目目录等去重后的可用快捷入口。
}
