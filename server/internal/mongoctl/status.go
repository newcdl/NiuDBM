package mongoctl

import (
	"context"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/bson"

	"niudbm/server/internal/mysqlctl"
)

func (s Spec) Status() ([]mysqlctl.StatusMetric, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	client, err := s.client(ctx)
	if err != nil {
		return nil, translate(err)
	}
	defer client.Disconnect(context.Background())

	var server struct {
		Connections struct {
			Current int `bson:"current"`
		} `bson:"connections"`
		Opcounters struct {
			Query  int64 `bson:"query"`
			Insert int64 `bson:"insert"`
			Update int64 `bson:"update"`
			Delete int64 `bson:"delete"`
		} `bson:"opcounters"`
		Uptime int64 `bson:"uptime"`
	}
	if err := client.Database("admin").RunCommand(ctx, bson.D{{Key: "serverStatus", Value: 1}}).Decode(&server); err != nil {
		return nil, translate(err)
	}
	var build struct {
		Version string `bson:"version"`
	}
	_ = client.Database("admin").RunCommand(ctx, bson.D{{Key: "buildInfo", Value: 1}}).Decode(&build)
	return []mysqlctl.StatusMetric{
		{Key: "version", Label: "MongoDB 版本", Value: build.Version},
		{Key: "connections", Label: "当前连接", Value: fmt.Sprintf("%d", server.Connections.Current)},
		{Key: "opcounters", Label: "累计操作", Value: fmt.Sprintf("查询 %d · 写入 %d", server.Opcounters.Query, server.Opcounters.Insert+server.Opcounters.Update+server.Opcounters.Delete)},
		{Key: "uptime", Label: "运行时间", Value: fmt.Sprintf("%d 小时", server.Uptime/3600)},
	}, nil
}
