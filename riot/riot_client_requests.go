package riot

import (
	"fmt"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/igtimi/go-igtimi/gen/go/com/igtimi"
)

var id atomic.Uint64

func requestId() string {
	var res uint64
	for {
		res = id.Load()
		if id.CompareAndSwap(res, res+1) {
			break
		}
	}
	return strconv.FormatUint(res, 16)
}

func (c *Connection) login(token *igtimi.Token) error {
	res, err := c.request(&igtimi.Msg{
		Msg: &igtimi.Msg_ChannelManagement{
			ChannelManagement: &igtimi.ChannelManagement{
				Mgmt: &igtimi.ChannelManagement_Auth{
					Auth: &igtimi.Authentication{
						Auth: &igtimi.Authentication_AuthRequest_{
							AuthRequest: &igtimi.Authentication_AuthRequest{
								Timestamp: uint64(time.Now().UnixMilli()),
								Token:     token,
							},
						},
					},
				},
			},
		},
	})
	if err != nil {
		return fmt.Errorf("auth failed: %w", err)
	}
	resp := res.GetChannelManagement().GetAuth().GetAuthResponse()
	if resp.Code != 200 {
		return fmt.Errorf("auth failed: %s", resp.Reason)
	}
	return nil
}

func (c *Connection) UserLogin(userToken string) error {
	return c.login(&igtimi.Token{
		Token: &igtimi.Token_UserToken{
			UserToken: userToken,
		},
	})
}

func (c *Connection) DeviceGroupLogin(devicegroupToken string) error {
	return c.login(&igtimi.Token{
		Token: &igtimi.Token_DeviceGroupToken{
			DeviceGroupToken: devicegroupToken,
		},
	})
}

func (c *Connection) Subscribe(req *igtimi.DataSubscription_SubscriptionRequest) error {
	req.Id = requestId()
	res, err := c.request(&igtimi.Msg{
		Msg: &igtimi.Msg_ChannelManagement{
			ChannelManagement: &igtimi.ChannelManagement{
				Mgmt: &igtimi.ChannelManagement_Subscription{
					Subscription: &igtimi.DataSubscription{
						Sub: &igtimi.DataSubscription_SubscriptionRequest_{
							SubscriptionRequest: req,
						},
					},
				},
			},
		},
	})
	if err != nil {
		return fmt.Errorf("subscription failed: %w", err)
	}
	resp := res.GetChannelManagement().GetSubscription().GetSubscriptionResponse()
	if resp.Code != 200 {
		return fmt.Errorf("subscription failed: %s", resp.Reason)
	}
	return nil
}

// set up channel to receive subscribed to messages
func (c *Connection) Receive() <-chan *igtimi.Data {
	c.receiverMutex.Lock()
	defer c.receiverMutex.Unlock()
	// buffer some messages to avoid missing them when reading/writing from the same goroutine
	ch := make(chan *igtimi.Data, 50)
	c.receivers = append(c.receivers, ch)
	return ch
}

func (c *Connection) apiRequest(req *igtimi.Request) (*igtimi.Response, error) {
	res, err := c.request(&igtimi.Msg{
		Msg: &igtimi.Msg_ApiData{
			ApiData: &igtimi.APIData{
				Data: &igtimi.APIData_Request{
					Request: req,
				},
			},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("api request failed: %w", err)
	}
	resp := res.GetApiData().GetResponse()
	if resp.Code != 200 {
		return nil, fmt.Errorf("api request failed: %s", resp.Reason)
	}
	return resp, nil
}

func (c *Connection) ListSessions() ([]*igtimi.Session, error) {
	resp, err := c.apiRequest(&igtimi.Request{
		Request: &igtimi.Request_Sessions{
			Sessions: &igtimi.SessionsRequest{
				Request: &igtimi.SessionsRequest_ListSessions_{
					ListSessions: &igtimi.SessionsRequest_ListSessions{},
				},
			},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("ListSessions: %w", err)
	}
	return resp.GetSessions().Session, nil
}

func (c *Connection) ListDevices() ([]*igtimi.Device, error) {
	resp, err := c.apiRequest(&igtimi.Request{
		Request: &igtimi.Request_Devices{
			Devices: &igtimi.DevicesRequest{
				Request: &igtimi.DevicesRequest_ListDevices_{
					ListDevices: &igtimi.DevicesRequest_ListDevices{},
				},
			},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("ListDevices: %w", err)
	}
	return resp.GetDevices().Device, nil
}

func (c *Connection) SendData(data []*igtimi.DataMsg) error {
	err := c.send(&igtimi.Msg{
		Msg: &igtimi.Msg_Data{
			Data: &igtimi.Data{
				Data: data,
			},
		},
	})
	if err != nil {
		return fmt.Errorf("SendData: %w", err)
	}
	return nil
}
