package handler

import (
	"bytes"
	"com.fibonacci.crowd/http/resp"
	"com.fibonacci.crowd/utils"
	"github.com/dchest/captcha"
	"github.com/gofiber/fiber/v2"
	"net/http"
	"path"
	"strings"
)

//CaptchaHandler 图形验证码处理器
type CaptchaHandler struct{}

func NewCaptchaHandler() *CaptchaHandler {
	return &CaptchaHandler{}
}

//GetCaptcha 创建图形验证码
func (h *CaptchaHandler) GetCaptcha(c *fiber.Ctx) error {
	d := struct {
		CaptchaId string
	}{
		captcha.New(),
	}

	var response resp.CaptchaResponse
	if d.CaptchaId != "" {
		response.CaptchaId = d.CaptchaId
		response.ImageUrl = "/show/" + d.CaptchaId + ".png"
	} else {

		return c.Send(utils.JsonFail("创建失败"))
	}

	return c.Send(utils.JsonOk(response))
}

//VerifyCaptcha 验证码校验
func (h *CaptchaHandler) VerifyCaptcha(c *fiber.Ctx) error {
	captchaId := c.Query("captchaId")
	captchaSolution := c.Query("captchaSolution")
	if captchaId == "" || captchaSolution == "" {
		return c.Send(utils.JsonFail("参数错误"))
	}

	if !captcha.VerifyString(captchaId, captchaSolution) {
		return c.Send(utils.JsonFail("验证码错误"))
	}

	return c.Send(utils.JsonOk("校验成功"))
}

//GetCaptchaPng 获取验证码 图片
func (h *CaptchaHandler) GetCaptchaPng(c *fiber.Ctx) error {
	return ServeHTTP(c)
}

func Serve(c *fiber.Ctx, id, ext, lang string, download bool, width, height int) error {
	c.Set("Cache-Control", "no-cache, no-store, must-revalidate")
	c.Set("Pragma", "no-cache")
	c.Set("Expires", "0")

	var content bytes.Buffer
	switch ext {
	case ".png":
		c.Set("Content-Type", "image/png")
		captcha.WriteImage(&content, id, width, height)
	case ".wav":
		c.Set("Content-Type", "audio/x-wav")
		captcha.WriteAudio(&content, id, lang)
	default:
		return captcha.ErrNotFound
	}

	if download {
		c.Set("Content-Type", "application/octet-stream")
	}

	return c.Send(content.Bytes())
}

func ServeHTTP(c *fiber.Ctx) error {
	uri := c.Context().URI().Path()
	dir, file := path.Split(string(uri))
	ext := path.Ext(file)
	id := file[:len(file)-len(ext)]
	if ext == "" || id == "" {
		return c.SendStatus(http.StatusNotFound)
	}
	if c.FormValue("reload") != "" {
		captcha.Reload(id)
	}
	lang := strings.ToLower(c.FormValue("lang"))
	download := path.Base(dir) == "download"
	if Serve(c,  id, ext, lang, download, captcha.StdWidth, captcha.StdHeight) == captcha.ErrNotFound {
		return c.SendStatus(http.StatusNotFound)
	}
	return nil
}
