package mtproto

import (
	"context"
	"os"

	"github.com/go-faster/errors"
	tdauth "github.com/gotd/td/telegram/auth"
	tdtelegram "github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"
	tgtelegram "github.com/thedavidweng/tg-drive-cli/internal/telegram"
)

type callbackAuth struct {
	phone      string
	codeFn     func() (string, error)
	passwordFn func() (string, error)
}

func (a callbackAuth) Phone(_ context.Context) (string, error) { return a.phone, nil }

func (a callbackAuth) Password(_ context.Context) (string, error) {
	if a.passwordFn == nil {
		return "", tdauth.ErrPasswordNotProvided
	}
	return a.passwordFn()
}

func (a callbackAuth) Code(_ context.Context, _ *tg.AuthSentCode) (string, error) {
	return a.codeFn()
}

func (a callbackAuth) AcceptTermsOfService(_ context.Context, tos tg.HelpTermsOfService) error {
	return nil
}

func (a callbackAuth) SignUp(_ context.Context) (tdauth.UserInfo, error) {
	return tdauth.UserInfo{}, errors.New("sign up not supported")
}

func (c *Client) Login(ctx context.Context, apiID int64, apiHash, phone string, codeFn, passwordFn func() (string, error)) (*tgtelegram.User, error) {
	if apiID != 0 {
		c.apiID = int(apiID)
	}
	if apiHash != "" {
		c.apiHash = apiHash
	}
	var out *tgtelegram.User
	err := c.run(ctx, func(ctx context.Context, _ *tg.Client, client *tdtelegram.Client) error {
		flow := tdauth.NewFlow(callbackAuth{
			phone:      phone,
			codeFn:     codeFn,
			passwordFn: passwordFn,
		}, tdauth.SendCodeOptions{})
		if err := client.Auth().IfNecessary(ctx, flow); err != nil {
			return mapRPCError(err)
		}
		self, err := client.Self(ctx)
		if err != nil {
			return mapRPCError(err)
		}
		out = &tgtelegram.User{
			ID:          self.ID,
			Phone:       phoneNumber(self),
			DisplayName: userDisplay(self),
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *Client) Status(ctx context.Context) (*tgtelegram.User, bool, error) {
	var out *tgtelegram.User
	err := c.run(ctx, func(ctx context.Context, _ *tg.Client, client *tdtelegram.Client) error {
		status, err := client.Auth().Status(ctx)
		if err != nil {
			return mapRPCError(err)
		}
		if !status.Authorized {
			return nil
		}
		self, err := client.Self(ctx)
		if err != nil {
			return mapRPCError(err)
		}
		out = &tgtelegram.User{
			ID:          self.ID,
			Phone:       phoneNumber(self),
			DisplayName: userDisplay(self),
		}
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	if out == nil {
		return nil, false, nil
	}
	return out, true, nil
}

func (c *Client) Logout(ctx context.Context) error {
	return c.run(ctx, func(ctx context.Context, api *tg.Client, _ *tdtelegram.Client) error {
		if _, err := api.AuthLogOut(ctx); err != nil {
			return mapRPCError(err)
		}
		_ = os.Remove(c.sessionPath)
		return nil
	})
}
