package main

import (
	"fmt"
	"github.com/gin-gonic/gin"
	"github.com/spf13/viper"
	"log"
	"thuhole-go-backend/pkg/config"
	"thuhole-go-backend/pkg/consts"
	"thuhole-go-backend/pkg/db"
	"thuhole-go-backend/pkg/logger"
	"thuhole-go-backend/pkg/route"
	"thuhole-go-backend/pkg/utils"
	"time"
)
//这里是登录的入口
func main() {
	//pkg文件夹中的logger.go里InitLog函数
	//consts.LoginApiLogFile = "login-api.log"
	//将打开文件login-api.log的操作写入日志
	logger.InitLog(consts.LoginApiLogFile)
	//初始化config
	config.InitConfigFile()

	fmt.Print("Read salt from stdin: ")
	//下划线：忽略返回值
	//Scanln 将成功读取的空白分隔的值保存进成功传递给本函数的参数
	//		 换行时停止扫描
	//&utils.Salt 默认为空String的地址 &为取地址
	_, _ = fmt.Scanln(&utils.Salt)
	if utils.Hash1(utils.Salt) != viper.GetString("salt_hashed") {
		panic("salt verification failed!")
	}

	db.InitDb()

	log.Println("start time: ", time.Now().Format("01-02 15:04:05"))
	if false == viper.GetBool("is_debug") {
		gin.SetMode(gin.ReleaseMode)
	}

	route.LoginApiListenHttp()
}
