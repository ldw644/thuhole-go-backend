package route

import (
	"bytes"
	"encoding/base64"
	"github.com/gin-gonic/gin"
	"github.com/spf13/viper"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"thuhole-go-backend/pkg/consts"
	"thuhole-go-backend/pkg/db"
	"thuhole-go-backend/pkg/s3"
	"thuhole-go-backend/pkg/utils"
)

func generateTag(text string) string {
	re := regexp.MustCompile(`[#＃](性相关|性话题|政治相关|政治话题|NSFW|nsfw|折叠)`)
	if re.MatchString(text) {
		return strings.ToUpper(re.FindStringSubmatch(text)[1])
	}
	re1, err := regexp.Compile(viper.GetString("fold_regex"))
	if err == nil && re1.MatchString(text) {
		return "折叠"
	}
	re2, err2 := regexp.Compile(viper.GetString("sex_related_regex"))
	if err2 == nil && re2.MatchString(text) {
		return "性相关"
	}
	return ""
}

func doPost(c *gin.Context) {
	text := c.PostForm("text")
	typ := c.PostForm("type")
	token := c.PostForm("user_token")
	img := c.PostForm("data")
	if len(text) > consts.PostMaxLength {
		utils.HttpReturnWithCodeOne(c, "字数过长！字数限制为"+strconv.Itoa(consts.PostMaxLength)+"字节。")
		return
	} else if len(text) == 0 && typ == "text" {
		utils.HttpReturnWithCodeOne(c, "请输入内容")
		return
	} else if typ != "text" && typ != "image" {
		utils.HttpReturnWithCodeOne(c, "未知类型的树洞")
		return
	} else if int(float64(len(img))/consts.Base64Rate) > consts.ImgMaxLength {
		utils.HttpReturnWithCodeOne(c, "图片大小超出限制！")
		return
	}

	emailHash, err3 := db.GetInfoByToken(token)
	if err3 != nil {
		utils.HttpReturnWithCodeOne(c, "发送失败，请检查登录状态")
		return
	}
	timestamp := int(utils.GetTimeStamp())
	bannedTimes, _ := db.BannedTimesPost(emailHash, timestamp)
	if bannedTimes > 0 {
		utils.HttpReturnWithCodeOne(c, "很抱歉，您当前处于禁言状态，无法发送树洞。")
		return
	}

	var pid int
	var err error
	var imgPath string
	if typ == "image" {
		imgPath = utils.GenToken()
		sDec, err2 := base64.StdEncoding.DecodeString(img)
		if err2 != nil {
			c.JSON(http.StatusOK, gin.H{
				"code": 1,
				"msg":  "发送失败，图片数据不合法",
			})
			return
		}
		fileType := http.DetectContentType(sDec)
		if fileType != "image/jpeg" && fileType != "image/jpg" {
			c.JSON(http.StatusOK, gin.H{
				"code": 1,
				"msg":  "发送失败，图片数据不合法",
			})
			return
		}
		hashedPath := filepath.Join(viper.GetString("images_path"), imgPath[:2])
		_ = os.MkdirAll(hashedPath, os.ModePerm)
		err3 := ioutil.WriteFile(filepath.Join(hashedPath, imgPath+".jpeg"), sDec, 0644)
		if err3 != nil {
			c.JSON(http.StatusOK, gin.H{
				"code": 1,
				"msg":  "图片写入失败，请联系管理员",
			})
			return
		}

		pid, err = db.SavePost(emailHash, text, generateTag(text), typ, imgPath+".jpeg")
		if err == nil && len(viper.GetString("DCSecretKey")) > 0 {
			err4 := s3.Upload(imgPath[:2]+"/"+imgPath+".jpeg", bytes.NewReader(sDec))
			if err4 != nil {
				log.Printf("S3 upload failed, err=%s\n", err4)
			}
		}
	} else {
		pid, err = db.SavePost(emailHash, text, generateTag(text), typ, "")
	}

	if err != nil {
		log.Printf("error dbSavePost! %s\n", err)
		c.JSON(http.StatusOK, gin.H{
			"code": 1,
			"msg":  "数据库写入失败，请联系管理员",
		})
		return
	} else {
		c.JSON(http.StatusOK, gin.H{
			"code": 0,
			"data": pid,
		})
		_, _ = db.AddAttentionIns.Exec(emailHash, pid)
		_, _ = db.PlusOneAttentionIns.Exec(pid)
		return
	}
}

func doComment(c *gin.Context) {
	text := c.PostForm("text")
	typ := c.PostForm("type")
	token := c.PostForm("user_token")
	img := c.PostForm("data")
	if typ != "image" {
		typ = "text"
	}
	if len(text) > consts.CommentMaxLength {
		utils.HttpReturnWithCodeOne(c, "字数过长！字数限制为"+strconv.Itoa(consts.CommentMaxLength)+"字节。")
		return
	} else if len(text) == 0 && typ == "text" {
		utils.HttpReturnWithCodeOne(c, "请输入内容")
		return
	} else if int(float64(len(img))/consts.Base64Rate) > consts.ImgMaxLength {
		utils.HttpReturnWithCodeOne(c, "图片大小超出限制！")
		return
	}
	pid, err := strconv.Atoi(c.PostForm("pid"))
	if err != nil {
		utils.HttpReturnWithCodeOne(c, "发送失败，pid不合法")
		return
	}
	emailHash, err5 := db.GetInfoByToken(token)
	if err5 != nil {
		utils.HttpReturnWithCodeOne(c, "发送失败，请检查登录状态")
		return
	}
	timestamp := int(utils.GetTimeStamp())
	bannedTimes, _ := db.BannedTimesPost(emailHash, timestamp)
	if bannedTimes > 0 {
		utils.HttpReturnWithCodeOne(c, "很抱歉，您当前处于禁言状态，无法发送评论。")
		return
	}
	var dzEmailHash string
	dzEmailHash, _, _, _, _, _, _, _, _, err = db.GetOnePost(pid)
	if err != nil {
		utils.HttpReturnWithCodeOne(c, "发送失败，pid不存在")
		return
	}

	var name string
	if dzEmailHash == emailHash {
		name = consts.DzName
	} else {
		name, err = db.GetCommentNameByEmailHash(emailHash, pid)
		if err != nil { // token is not in comments
			var i int
			i, err = db.GetCommentCount(pid, dzEmailHash)
			if err != nil {
				c.JSON(http.StatusOK, gin.H{
					"code": 1,
					"msg":  "数据库读取失败，请联系管理员",
				})
				return
			}
			name = utils.GetCommenterName(i + 1)
		}
	}

	var imgPath string
	if typ == "image" {
		imgPath = utils.GenToken()
		sDec, err2 := base64.StdEncoding.DecodeString(img)
		if err2 != nil {
			c.JSON(http.StatusOK, gin.H{
				"code": 1,
				"msg":  "发送失败，图片数据不合法",
			})
			return
		}
		fileType := http.DetectContentType(sDec)
		if fileType != "image/jpeg" && fileType != "image/jpg" {
			c.JSON(http.StatusOK, gin.H{
				"code": 1,
				"msg":  "发送失败，图片数据不合法",
			})
			return
		}
		hashedPath := filepath.Join(viper.GetString("images_path"), imgPath[:2])
		_ = os.MkdirAll(hashedPath, os.ModePerm)
		err3 := ioutil.WriteFile(filepath.Join(hashedPath, imgPath+".jpeg"), sDec, 0644)
		if err3 != nil {
			c.JSON(http.StatusOK, gin.H{
				"code": 1,
				"msg":  "图片写入失败，请联系管理员",
			})
			return
		}

		_, err = db.SaveComment(emailHash, text, "", typ, imgPath+".jpeg", pid, name)
		if err == nil && len(viper.GetString("DCSecretKey")) > 0 {
			err4 := s3.Upload(imgPath[:2]+"/"+imgPath+".jpeg", bytes.NewReader(sDec))
			if err4 != nil {
				log.Printf("S3 upload failed, err=%s\n", err4)
			}
		}
	} else {
		_, err = db.SaveComment(emailHash, text, "", "text", "", pid, name)
	}
	//_, err = db.SaveComment(emailHash, text, "", "text", "", pid, name)

	if err != nil {
		utils.HttpReturnWithCodeOne(c, "数据库写入失败，请联系管理员")
		return
	} else {
		c.JSON(http.StatusOK, gin.H{
			"code": 0,
			"data": pid,
		})

		_, err = db.PlusOneCommentIns.Exec(pid)
		if err != nil {
			log.Printf("error plusOneCommentIns while commenting: %s\n", err)
		}
		isAttention, err := db.IsAttention(emailHash, pid)
		if err == nil && isAttention == 0 {
			_, _ = db.AddAttentionIns.Exec(emailHash, pid)
			_, _ = db.PlusOneAttentionIns.Exec(pid)
		}

		// set tag
		if dzEmailHash == emailHash {
			re := regexp.MustCompile(`[#＃](性相关|性话题|政治相关|政治话题|NSFW|nsfw|折叠|重复内容)`)
			if re.MatchString(text) {
				tag := strings.ToUpper(re.FindStringSubmatch(text)[1])
				_, _ = db.SetPostTagIns.Exec(tag, pid)
			}
		}
	}
}

func doReport(c *gin.Context) {
	reason := c.PostForm("reason")
	if len(reason) > consts.ReportMaxLength {
		utils.HttpReturnWithCodeOne(c, "字数过长！字数限制为"+strconv.Itoa(consts.ReportMaxLength)+"字节。")
		return
	} else if len(reason) == 0 {
		utils.HttpReturnWithCodeOne(c, "请输入内容")
		return
	}
	pid, err := strconv.Atoi(c.PostForm("pid"))
	if err != nil {
		utils.HttpReturnWithCodeOne(c, "举报失败，pid不合法")
		return
	} else if _, ok := utils.ContainsInt(viper.GetIntSlice("disallow_report_pids"), pid); ok {
		utils.HttpReturnWithCodeOne(c, "举报失败，哈哈")
		return
	}
	token := c.PostForm("user_token")
	dzEmailHash, text, _, _, typ, _, _, _, reportnum, err2 := db.GetOnePost(pid)
	if err2 != nil {
		utils.HttpReturnWithCodeOne(c, "举报失败，pid不存在")
		return
	}
	emailHash, err5 := db.GetInfoByToken(token)
	if err5 != nil {
		utils.HttpReturnWithCodeOne(c, "举报失败，请检查登录状态")
		return
	}
	_, err = db.SaveReport(emailHash, reason, pid)
	if err != nil {
		utils.HttpReturnWithCodeOne(c, "举报失败")
		return
	} else {
		if reportnum == 9 {
			_, err = db.PlusReportIns.Exec(1, pid)
			if err != nil {
				log.Printf("error plusOneReportIns while reporting: %s\n", err)
			}
			//禁言
			bannedTimes, _ := db.BannedTimesPost(dzEmailHash, -1)
			err = db.SaveBanUser(dzEmailHash,
				"您的"+typ+"树洞#"+strconv.Itoa(pid)+"\n\""+text+"\"\n由于用户举报过多被删除。这是您第"+
					strconv.Itoa(bannedTimes+1)+"次被举报，在"+strconv.Itoa(bannedTimes+1)+"天之内您将无法发布树洞。",
				(1+bannedTimes)*86400)
			if err != nil {
				log.Printf("error dbSaveBanUser while reporting: %s\n", err)
			}
		} else if _, isAdmin := utils.ContainsString(viper.GetStringSlice("admins_tokens"), token); isAdmin {
			_, err = db.PlusReportIns.Exec(666, pid)
			if err != nil {
				log.Printf("error plus666ReportIns while reporting: %s\n", err)
			}
			bannedTimes, _ := db.BannedTimesPost(dzEmailHash, -1)
			err = db.SaveBanUser(dzEmailHash,
				"您的"+typ+"树洞#"+strconv.Itoa(pid)+"\n\""+text+"\"\n被管理员删除。管理员的删除理由是：【"+reason+"】。这是您第"+
					strconv.Itoa(bannedTimes+1)+"次被举报，在"+strconv.Itoa(bannedTimes+1)+"天之内您将无法发布树洞。",
				(1+bannedTimes)*86400)
			if err != nil {
				log.Printf("error dbSaveBanUser while reporting: %s\n", err)
			}
		} else {
			_, err = db.PlusReportIns.Exec(1, pid)
			if err != nil {
				log.Printf("error plusOneReportIns while reporting: %s\n", err)
			}
		}

		if err != nil {
			utils.HttpReturnWithCodeOne(c, "举报失败，数据库写入失败，请联系管理员")
			return
		} else {
			c.JSON(http.StatusOK, gin.H{
				"code": 0,
			})
		}
	}
}

func doAttention(c *gin.Context) {
	pid, err := strconv.Atoi(c.PostForm("pid"))
	if err != nil {
		utils.HttpReturnWithCodeOne(c, "关注操作失败，pid不合法")
		return
	}
	_, _, _, _, _, _, _, _, _, err3 := db.GetOnePost(pid)
	if err3 != nil {
		utils.HttpReturnWithCodeOne(c, "关注失败，pid不存在")
		return
	}
	s := c.PostForm("switch")
	token := c.PostForm("user_token")
	emailHash, err5 := db.GetInfoByToken(token)
	if err5 != nil {
		utils.HttpReturnWithCodeOne(c, "关注失败，请检查登录状态")
		return
	}
	isAttention, err2 := db.IsAttention(emailHash, pid)
	if err2 != nil {
		log.Printf("error dbIsAttention while doAttention: %s\n", err2)
		utils.HttpReturnWithCodeOne(c, "数据库读取失败，请联系管理员")
		return
	}
	if isAttention == 0 && s == "0" {
		utils.HttpReturnWithCodeOne(c, "您已经取消关注了")
		return
	}
	if isAttention == 1 && s == "1" {
		utils.HttpReturnWithCodeOne(c, "您已经关注过了")
		return
	}
	if isAttention == 0 {
		_, _ = db.AddAttentionIns.Exec(emailHash, pid)
		_, _ = db.PlusOneAttentionIns.Exec(pid)
	}
	if isAttention == 1 {
		_, _ = db.RemoveAttentionIns.Exec(emailHash, pid)
		_, _ = db.MinusOneAttentionIns.Exec(pid)
	}

	c.JSON(http.StatusOK, gin.H{
		"code": 0,
	})
}

func apiPost(c *gin.Context) {
	action := c.Query("action")
	switch action {
	case "docomment":
		doComment(c)
		return
	case "dopost":
		doPost(c)
		return
	case "attention":
		doAttention(c)
		return
	case "report":
		doReport(c)
		return
	default:
		c.AbortWithStatus(403)
	}
}
