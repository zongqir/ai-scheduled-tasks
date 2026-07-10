package wechat

import (
	"strings"
	"testing"

	"ai-sched-cli/internal/channel"
)

func TestRenderTextFormatsWechatMessage(t *testing.T) {
	rendered := renderText(channel.Message{
		Title:    "检查 CI",
		Body:     "执行结果：成功\n任务：检查 CI\n\n输出结果：\nOK",
		Priority: "high",
	})

	if !strings.Contains(rendered, "任务通知\n检查 CI") {
		t.Fatalf("unexpected rendered header: %q", rendered)
	}
	if !strings.Contains(rendered, "执行结果：成功") {
		t.Fatalf("expected body in rendered text: %q", rendered)
	}
	if !strings.Contains(rendered, "优先级：高") {
		t.Fatalf("expected priority label in rendered text: %q", rendered)
	}
}

func TestRenderTextAllowsBodyOnly(t *testing.T) {
	rendered := renderText(channel.Message{
		Body: "提醒事项\n任务：买菜",
	})

	if !strings.HasPrefix(rendered, "任务通知\n\n提醒事项") {
		t.Fatalf("unexpected body-only render: %q", rendered)
	}
}
