package db

// 仅测试用：暴露可注入迁移集的内部入口与嵌入迁移文件系统。
var MigrateFS = migrate
var EmbeddedMigrations = migrationsFS
var PathToURI = pathToURI
var CleanupPlaintextBackups = cleanupPlaintextBackups
