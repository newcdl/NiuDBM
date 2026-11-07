package images

import "testing"

func TestFamilyTagCollision(t *testing.T) {
	mysql, ok := FindFamily(FamilyMySQL, "8.0")
	if !ok || mysql.Name != "mysql:8.0" {
		t.Fatalf("MySQL 8.0 解析错误：%+v", mysql)
	}
	mongo, ok := FindFamily(FamilyMongo, "8.0")
	if !ok || mongo.Name != "mongo:8.0" {
		t.Fatalf("MongoDB 8.0 解析错误：%+v", mongo)
	}
	if mysql.Name == mongo.Name {
		t.Fatal("同名 tag 不能解析成同一个镜像")
	}
}
