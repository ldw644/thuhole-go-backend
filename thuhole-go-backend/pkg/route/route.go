package route

import (
	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	libredis "github.com/go-redis/redis/v7"
	"github.com/spf13/viper"
	"github.com/ulule/limiter/v3"
	sredis "github.com/ulule/limiter/v3/drivers/store/redis"
	"log"
	"net"
	"net/http"
	"strings"
	"thuhole-go-backend/pkg/db"
	"thuhole-go-backend/pkg/mail"
	"thuhole-go-backend/pkg/recaptcha"
	"thuhole-go-backend/pkg/utils"
	"time"
)

var lmt *limiter.Limiter

func initLimiter() {
	rate := limiter.Rate{
		Period: 24 * time.Hour,
		Limit:  viper.GetInt64("max_email_per_ip_per_day"),
	}
	option, err := libredis.ParseURL(viper.GetString("redis_source"))
	if err != nil {
		utils.FatalErrorHandle(&err, "failed init redis url")
		return
	}
	client := libredis.NewClient(option)
	store, err2 := sredis.NewStoreWithOptions(client, limiter.StoreOptions{
		Prefix:   "email_limiter",
		MaxRetry: 3,
	})
	if err2 != nil {
		utils.FatalErrorHandle(&err2, "failed init redis store")
		return
	}
	lmt = limiter.New(store, rate)
}

func sendCode(c *gin.Context) {
	code := utils.GenCode()
	user := c.Query("user")
	recaptchaToken := c.Query("recaptcha_token")
	if recaptchaToken == "" || recaptchaToken == "undefined" {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"msg":     "recaptcha校验失败，请稍等片刻或刷新重试。如果注册持续失败，可邮件联系thuhole@protonmail.com人工注册。",
		})
		return
	}

	if !(strings.HasSuffix(user, "@mails.tsinghua.edu.cn")) || !utils.CheckEmail(user) {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"msg":     "很抱歉，您的邮箱无法注册T大树洞。目前只有@mails.tsinghua.edu.cn的邮箱开放注册。",
		})
		return
	}

	hashedUser := utils.HashEmail(user)
	if _, b := utils.ContainsString(viper.GetStringSlice("bannedEmailHashes"), hashedUser); b {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"msg":     "您的账户已被冻结。如果需要解冻，请联系thuhole@protonmail.com。",
		})
		return
	}
	now := utils.GetTimeStamp()
	_, timeStamp, _, err := db.GetCode(hashedUser)
	//if err != nil {
	//	log.Printf("dbGetCode failed when sendCode: %s\n", err)
	//}
	if now-timeStamp < 300 {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"msg":     "请不要短时间内重复发送邮件。",
		})
		return
	}

	context, err2 := lmt.Get(c, c.ClientIP())
	if err2 != nil {
		log.Printf("send mail to %s failed, limiter fatal error. IP=%s,err=%s\n", user, c.ClientIP(), err2)
		c.AbortWithStatus(500)
		return
	}

	if context.Reached {
		log.Printf("send mail to %s failed, too many requests. IP=%s,err=%s\n", user, c.ClientIP(), err)
		c.JSON(http.StatusTooManyRequests, gin.H{
			"success": false,
			"msg":     "您今天已经发送了过多验证码，请24小时之后重试。",
		})
		return
	}

	if utils.GeoDb != nil && len(viper.GetStringSlice("allowed_register_countries")) != 0 {
		ip := net.ParseIP(c.ClientIP())
		record, err5 := utils.GeoDb.Country(ip)
		if err5 == nil {
			country := record.Country.Names["zh-CN"]
			if _, ok := utils.ContainsString(viper.GetStringSlice("allowed_register_countries"), country); !ok {
				log.Println("register not allowed:", c.ClientIP(), country, user)
				c.JSON(http.StatusOK, gin.H{
					"success": false,
					"msg":     "您所在的国家暂未开放注册。",
				})
				return
			}
		}
	}

	captcha, _ := recaptcha.NewReCAPTCHA(viper.GetString("recaptcha_private_key"), recaptcha.V3, 10*time.Second)
	err = captcha.VerifyWithOptions(recaptchaToken, recaptcha.VerifyOption{
		RemoteIP:  c.ClientIP(),
		Threshold: float32(viper.GetFloat64("recaptcha_threshold")),
	})
	if err != nil {
		log.Println("recaptcha server error", err, c.ClientIP(), user)
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"msg":     "recaptcha风控系统校验失败，请检查网络环境并刷新重试。如果注册持续失败，可邮件联系thuhole@protonmail.com人工注册。",
		})
		return
	}

	err = mail.SendMail(code, user)
	if err != nil {
		log.Printf("send mail to %s failed: %s\n", user, err)
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"msg":     "验证码邮件发送失败。",
		})
		return
	}

	err = db.SaveCode(hashedUser, code)
	if err != nil {
		log.Printf("save code failed: %s\n", err)
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"msg":     "数据库写入失败，请联系管理员",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"msg":     "验证码发送成功。如果要在多客户端登录请不要使用邮件登录而是Token登录。5分钟内无法重复发送验证码，请记得查看垃圾邮件。",
	})
}

func login(c *gin.Context) {
	user := c.Query("user")
	code := c.Query("valid_code")
	hashedUser := utils.HashEmail(user)
	if _, b := utils.ContainsString(viper.GetStringSlice("bannedEmailHashes"), hashedUser); b {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"msg":     "您的账户已被冻结。如果需要解冻，请联系thuhole@protonmail.com。",
		})
		return
	}
	now := utils.GetTimeStamp()

	if !(strings.HasSuffix(user, "@mails.tsinghua.edu.cn")) || !utils.CheckEmail(user) {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"msg":     "邮箱格式不正确",
		})
		return
	}

	correctCode, timeStamp, failedTimes, err := db.GetCode(hashedUser)
	if err != nil {
		log.Printf("check code failed: %s\n", err)
	}
	if failedTimes >= 10 && now-timeStamp <= 43200 {
		c.JSON(http.StatusOK, gin.H{
			"code": 1,
			"msg":  "验证码错误尝试次数过多，请重新发送验证码",
		})
		return
	}
	if correctCode != code || now-timeStamp > 43200 {
		log.Printf("验证码无效或过期: %s, %s\n", user, code)
		c.JSON(http.StatusOK, gin.H{
			"code": 1,
			"msg":  "验证码无效或过期",
		})
		_, _ = db.PlusOneFailedCodeIns.Exec(hashedUser)
		return
	}
	token := utils.GenToken()
	err = db.SaveToken(token, hashedUser)
	if err != nil {
		log.Printf("failed dbSaveToken while login, %s\n", err)
		c.JSON(http.StatusOK, gin.H{
			"code": 1,
			"msg":  "数据库写入失败，请联系管理员",
		})
		return
	} else {
		c.JSON(http.StatusOK, gin.H{
			"code":       0,
			"msg":        "登录成功！",
			"user_token": token,
		})
		return
	}
}

func systemMsg(c *gin.Context) {
	c.Header("Content-Type", "application/json; charset=utf-8")
	token := c.Query("user_token")
	emailHash, err := db.GetInfoByToken(token)
	if err == nil {
		data, err2 := db.GetBannedMsgs(emailHash)
		if err2 != nil {
			log.Printf("dbGetBannedMsgs failed while systemMsg: %s\n", err2)
			utils.HttpReturnWithCodeOne(c, "数据库读取失败，请联系管理员")
			return
		} else {
			c.JSON(http.StatusOK, gin.H{
				"error":  nil,
				"result": data,
			})
		}
	} else {
		//log.Printf("check token failed: %s\n", err)
		c.String(http.StatusOK, `{"error":null,"result":[]}`)
	}
}

//func ListenHttp() {
//	r := gin.Default()
//	r.Use(cors.Default())
//
//	initLimiter()
//
//	r.POST("/api_xmcp/login/send_code", sendCode)
//	r.POST("/api_xmcp/login/login", login)
//	r.GET("/api_xmcp/hole/system_msg", systemMsg)
//	r.GET("/services/thuhole/api.php", apiGet)
//	r.POST("/services/thuhole/api.php", apiPost)
//	_ = r.Run(consts.ListenAddress)
//}

func ServicesApiListenHttp() {
	r := gin.Default()
	r.Use(cors.Default())

	r.GET("/api_xmcp/hole/system_msg", systemMsg)
	r.GET("/services/thuhole/api.php", apiGet)
	r.POST("/services/thuhole/api.php", apiPost)
	_ = r.Run(viper.GetString("services_api_listen_address"))
}

func LoginApiListenHttp() {
	r := gin.Default()
	r.Use(cors.Default())

	initLimiter()

	r.POST("/api_xmcp/login/send_code", sendCode)
	r.POST("/api_xmcp/login/login", login)
	_ = r.Run(viper.GetString("login_api_listen_address"))
}
