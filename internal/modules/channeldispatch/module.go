package channeldispatch

import (
	"context"
	"fmt"

	cordis "github.com/zeokeel/zeokeel-kernel"
	"github.com/zeokeel/zeokeel-kernel/module"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// ModuleID is the stable module identity used by assembly documents.
const ModuleID = "zeokeel/channeldispatch"

// Module joins the notification router with the channel adapters: the router
// decides where a message goes, the adapters know how to send it, and this is
// the thing that finally tries.
func Module() *module.Descriptor[Config] {
	return module.Define(ModuleID, func(ctx cordis.Context, cfg Config) error {
		router, err := ctx.Require(contracts.KeyNotifyRoute)
		if err != nil {
			return fmt.Errorf("%s: require notify router: %w", ModuleID, err)
		}

		var senders []contracts.ChannelSender
		if wechat, ok := ctx.Get(contracts.KeyWeChat); ok && wechat != nil {
			senders = append(senders, wechatSender{service: wechat})
		}
		if dingtalk, ok := ctx.Get(contracts.KeyDingTalk); ok && dingtalk != nil {
			senders = append(senders, dingtalkSender{service: dingtalk})
		}
		if feishu, ok := ctx.Get(contracts.KeyFeishu); ok && feishu != nil {
			senders = append(senders, feishuSender{service: feishu})
		}
		if mail, ok := ctx.Get(contracts.KeyEmail); ok && mail != nil {
			senders = append(senders, emailSender{service: mail})
		}

		service := New(router, senders, cfg.Targets, nil)
		if _, err := ctx.Provide(contracts.KeyChannelDispatch, contracts.ChannelDispatchService(service)); err != nil {
			return fmt.Errorf("%s: provide dispatch: %w", ModuleID, err)
		}
		return nil
	}).
		AddProvides(module.Provides(contracts.KeyChannelDispatch)).
		AddRequires(module.Requires(contracts.KeyNotifyRoute)).
		WithName("ZeoKeel channel dispatch")
}

// The adapters have different Send signatures; these thin shims give them the
// one shape the plan executor needs.
//
// The channel shims are typed with `plainTextSender`, not with the full service
// contract: dispatch only ever sends, so taking the narrow interface is what
// makes "outbound only" hold at compile time — a channel whose contract grows an
// inbound method still cannot be reached from here.

// plainTextSender is the only thing dispatch needs from a channel.
type plainTextSender interface {
	Send(ctx context.Context, conversation, text string) error
}

type wechatSender struct{ service contracts.WeChatService }

func (w wechatSender) Name() string { return contracts.NotifyChannelWeChat }
func (w wechatSender) Send(ctx context.Context, conversation, text string) error {
	return w.service.Send(ctx, conversation, text)
}

type dingtalkSender struct{ service plainTextSender }

func (d dingtalkSender) Name() string { return contracts.NotifyChannelDingTalk }
func (d dingtalkSender) Send(ctx context.Context, conversation, text string) error {
	return d.service.Send(ctx, conversation, text)
}

type feishuSender struct{ service plainTextSender }

func (f feishuSender) Name() string { return contracts.NotifyChannelFeishu }
func (f feishuSender) Send(ctx context.Context, conversation, text string) error {
	return f.service.Send(ctx, conversation, text)
}

type emailSender struct{ service contracts.EmailService }

func (e emailSender) Name() string { return contracts.NotifyChannelEmail }
func (e emailSender) Send(ctx context.Context, conversation, text string) error {
	return e.service.Send(ctx, conversation, "Bot 消息", text)
}
