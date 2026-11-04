// Package repository 实现原始归档与研究仓库；数据库驱动和 SQL 仅存在于这一层。
package repository

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	duckdb "github.com/duckdb/duckdb-go/v2"
	"gorm.io/gorm"
	"gorm.io/gorm/callbacks"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"
	"gorm.io/gorm/migrator"
	"gorm.io/gorm/schema"
)

// DuckDB 通过 GORM 操作内嵌引擎，底层复用官方 database/sql 驱动，不创建数据库工作进程。
// 单物理连接串行化读写事务，确保临时入库表与一致性快照不会互相干扰。
type DuckDB struct {
	Path   string     // DuckDB 文件路径，旁边的 exports 目录保存完整 Parquet 快照。
	mu     sync.Mutex // 只保护延迟初始化与关闭；连接排队由支持 context 的 database/sql 管理。
	db     *sql.DB    // 原生连接池，只用于生命周期管理，由 GORM 复用。
	orm    *gorm.DB   // 已完成版本迁移的 ORM 会话，模型及事务操作统一通过它执行。
	closed bool       // Close 后拒绝重新打开，避免退出过程中遗留文件句柄。
}

func NewDuckDB(path string) *DuckDB {
	// 路径在构造时固定，避免延迟初始化时改写公开字段，与并发 Prepare/查询产生竞态。
	if absolute, err := filepath.Abs(path); err == nil {
		path = absolute
	}
	return &DuckDB{Path: path}
}
func (d *DuckDB) Prepare() error { return os.MkdirAll(filepath.Dir(d.Path), 0755) }

func (d *DuckDB) open(ctx context.Context) (*gorm.DB, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if d.closed {
		return nil, errors.New("数据仓库已经关闭")
	}
	if d.db != nil {
		return d.orm, nil
	}
	path, err := filepath.Abs(d.Path)
	if err != nil {
		return nil, err
	}
	if err = os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return nil, err
	}
	// 每个新物理连接都设置审计时区，取消导致连接被替换后也不能依赖宿主时区。
	connector, err := duckdb.NewConnector(path, func(conn driver.ExecerContext) error {
		_, err := conn.ExecContext(context.Background(), "SET TimeZone='UTC'", nil)
		return err
	})
	if err != nil {
		return nil, err
	}
	db := sql.OpenDB(connector)
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	orm, err := gorm.Open(duckDBDialect{connection: db}, &gorm.Config{
		// 用例已有显式整批事务，不让每次 Create 再创建嵌套事务或 DuckDB 不支持的 SAVEPOINT。
		SkipDefaultTransaction: true, DisableNestedTransaction: true, DisableAutomaticPing: true,
		Logger: logger.Default.LogMode(logger.Silent), NowFunc: func() time.Time { return time.Now().UTC() },
	})
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	if err = migrate(ctx, orm); err != nil {
		_ = db.Close()
		return nil, err
	}
	d.db = db
	d.orm = orm
	return orm, nil
}

// transact 是仓库所有读写的事务边界：等待连接可取消，失败/取消回滚，提交成功才返回结果。
// 文件归档不属于此事务，调用方仍需在入库成功后才持久化下载断点。
func transact[T any](ctx context.Context, d *DuckDB, run func(*gorm.DB) (T, error)) (T, error) {
	var zero, value T
	db, err := d.open(ctx)
	if err != nil {
		return zero, err
	}
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var err error
		value, err = run(tx)
		if err != nil {
			return err
		}
		return ctx.Err()
	})
	if err != nil {
		return zero, err
	}
	return value, nil
}
func (d *DuckDB) Check(ctx context.Context) error { _, err := d.open(ctx); return err }

// Close 由组装根在任务结束后调用；sql.DB 负责释放连接与原生引擎资源。
func (d *DuckDB) Close() error {
	d.mu.Lock()
	d.closed = true
	db := d.db
	d.db = nil
	d.orm = nil
	d.mu.Unlock()
	if db != nil {
		return db.Close()
	}
	return nil
}

// duckDBDialect 只描述 DuckDB 的占位符、标识符和类型，模型读写使用 GORM 原生回调。
// 不重写 Create/Query 回调，确保批量 Create、复合主键和 OnConflict 等 ORM 语义完整保留。
// 表结构仍由带版本的迁移维护，不用 AutoMigrate 猜测并改写历史研究数据的精度或主键。
type duckDBDialect struct {
	connection *sql.DB // 组装好的官方 DuckDB Go 连接池；ORM 共享其事务、取消和生命周期。
}

func (duckDBDialect) Name() string { return "duckdb" }
func (d duckDBDialect) Initialize(db *gorm.DB) error {
	db.ConnPool = d.connection
	callbacks.RegisterDefaultCallbacks(db, &callbacks.Config{
		CreateClauses: []string{"INSERT", "VALUES", "ON CONFLICT"},
	})
	return nil
}
func (duckDBDialect) BindVarTo(w clause.Writer, _ *gorm.Statement, _ any) { _ = w.WriteByte('?') }
func (duckDBDialect) QuoteTo(w clause.Writer, identifier string) {
	for i, part := range strings.Split(identifier, ".") {
		if i > 0 {
			_ = w.WriteByte('.')
		}
		if part == "*" {
			_, _ = w.WriteString("*")
			continue
		}
		_ = w.WriteByte('"')
		_, _ = w.WriteString(strings.ReplaceAll(part, `"`, `""`))
		_ = w.WriteByte('"')
	}
}
func (duckDBDialect) Explain(statement string, vars ...any) string {
	return logger.ExplainSQL(statement, nil, "'", vars...)
}
func (duckDBDialect) DefaultValueOf(*schema.Field) clause.Expression {
	return clause.Expr{SQL: "DEFAULT"}
}
func (duckDBDialect) DataTypeOf(f *schema.Field) string {
	switch f.DataType {
	case schema.Bool:
		return "BOOLEAN"
	case schema.Int, schema.Uint:
		return "BIGINT"
	case schema.Float:
		return "DOUBLE"
	case schema.String:
		return "VARCHAR"
	case schema.Time:
		return "TIMESTAMP"
	case schema.Bytes:
		return "BLOB"
	default:
		return string(f.DataType)
	}
}
func (d duckDBDialect) Migrator(db *gorm.DB) gorm.Migrator {
	return migrator.Migrator{Config: migrator.Config{DB: db, Dialector: d}}
}
