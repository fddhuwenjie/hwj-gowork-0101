package repository

import (
	"database/sql"
	"strings"
	"time"
)

// timeToDB 将时间格式化为 UTC RFC3339Nano 文本入库，保证字典序与时间序一致。
func timeToDB(t time.Time) string {
	return t.UTC().Format(time.RFC3339Nano)
}

// dbToTime 解析入库时间文本。
func dbToTime(s string) (time.Time, error) {
	return time.Parse(time.RFC3339Nano, s)
}

// nullTimeToDB 处理可空时间。
func nullTimeToDB(t *time.Time) interface{} {
	if t == nil {
		return nil
	}
	return timeToDB(*t)
}

// nullTimeFromDB 将 sql.NullString 还原为 *time.Time。
func nullTimeFromDB(ns sql.NullString) (*time.Time, error) {
	if !ns.Valid || ns.String == "" {
		return nil, nil
	}
	t, err := dbToTime(ns.String)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// nowUTC 返回当前 UTC 时间（去掉单调时钟读数，便于持久化比较）。
func nowUTC() time.Time {
	return time.Now().UTC().Round(0)
}

// escapeLike 转义 LIKE 模式中的通配符，配合 ESCAPE '\' 使用。
func escapeLike(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(s)
}

// boolToInt 将布尔值转换为 SQLite 整数。
func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// IsUniqueViolation 判断错误是否由 UNIQUE 约束冲突引起，
// 用于幂等重放识别与重复提交检测。
func IsUniqueViolation(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}
