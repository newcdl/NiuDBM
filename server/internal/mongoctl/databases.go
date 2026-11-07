package mongoctl

import (
	"context"
	"errors"
	"io"
	"sort"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"

	"niudbm/server/internal/mysqlctl"
)

var systemDatabases = map[string]bool{
	"admin": true, "config": true, "local": true,
}

func ValidDBName(name string) error {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 63 {
		return errors.New("MongoDB 数据库名不能为空且最多 63 个字符")
	}
	if systemDatabases[name] {
		return errors.New(name + " 是 MongoDB 系统库，不能作为业务库")
	}
	if strings.ContainsAny(name, `/\."$*<>:|?;&'`) {
		return errors.New(`MongoDB 数据库名不能包含 / \ . " $ * < > : | ? ; & '`)
	}
	return nil
}

func (s Spec) ListDatabases() ([]mysqlctl.Database, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	client, err := s.client(ctx)
	if err != nil {
		return nil, translate(err)
	}
	defer client.Disconnect(context.Background())
	names, err := client.ListDatabaseNames(ctx, bson.D{})
	if err != nil {
		return nil, translate(err)
	}
	sort.Strings(names)
	out := make([]mysqlctl.Database, 0, len(names))
	for _, name := range names {
		if systemDatabases[name] {
			continue
		}
		collections, _ := client.Database(name).ListCollectionNames(ctx, bson.D{})
		var stats struct {
			DataSize int64 `bson:"dataSize"`
		}
		_ = client.Database(name).RunCommand(ctx, bson.D{{Key: "dbStats", Value: 1}}).Decode(&stats)
		out = append(out, mysqlctl.Database{
			Name:       name,
			TableCount: len(collections),
			SizeBytes:  stats.DataSize,
		})
	}
	return out, nil
}

func (s Spec) CountTables() (int, error) {
	dbs, err := s.ListDatabases()
	if err != nil {
		return 0, err
	}
	total := 0
	for _, db := range dbs {
		total += db.TableCount
	}
	return total, nil
}

func (s Spec) Charsets() ([]mysqlctl.Charset, error) { return []mysqlctl.Charset{}, nil }

func (s Spec) CreateDatabase(name, _ string) error {
	if err := ValidDBName(name); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	client, err := s.client(ctx)
	if err != nil {
		return translate(err)
	}
	defer client.Disconnect(context.Background())
	// MongoDB 的库在第一次写入集合时才真正存在；创建初始集合让空库也能立即出现在列表里。
	err = client.Database(name).CreateCollection(ctx, "_niudbm_bootstrap")
	if err != nil && !mongo.IsDuplicateKeyError(err) {
		var cmdErr mongo.CommandError
		if !errors.As(err, &cmdErr) || cmdErr.Code != 48 {
			return translate(err)
		}
	}
	return nil
}

func (s Spec) DropDatabase(name string) error {
	if err := ValidDBName(name); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	client, err := s.client(ctx)
	if err != nil {
		return translate(err)
	}
	defer client.Disconnect(context.Background())
	return translate(client.Database(name).Drop(ctx))
}

func (s Spec) Tables(dbName string) ([]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	client, err := s.client(ctx)
	if err != nil {
		return nil, translate(err)
	}
	defer client.Disconnect(context.Background())
	names, err := client.Database(dbName).ListCollectionNames(ctx, bson.D{})
	if err != nil {
		return nil, translate(err)
	}
	sort.Strings(names)
	return names, nil
}

func (s Spec) ExportCSV(io.Writer, string, string, int) (int64, error) {
	return 0, errors.New("MongoDB 暂不支持 CSV 导出，后续提供 JSON Lines 导出")
}

func (s Spec) ImportCSV(string, string, io.Reader) (int64, error) {
	return 0, errors.New("MongoDB 暂不支持 CSV 导入")
}

func (s Spec) ExecScript(string, string) (int, error) {
	return 0, errors.New("MongoDB 不支持 SQL 脚本")
}
