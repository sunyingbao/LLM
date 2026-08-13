//go:build !windows

package cloud

import (
	"fmt"

	"code.byted.org/overpass/ad_creative_aic_agent_coordinator/kitex_gen/base"
)

type baseRespGetter interface {
	GetBaseResp() *base.BaseResp
}

func rpcError(op string, resp baseRespGetter, err error) error {
	if resp != nil {
		if baseResp := resp.GetBaseResp(); baseResp != nil && baseResp.GetStatusCode() != 0 {
			msg := fmt.Sprintf("%s status_code=%d status_message=%q", op, baseResp.GetStatusCode(), baseResp.GetStatusMessage())
			if err != nil {
				return fmt.Errorf("%s: %w", msg, err)
			}
			return fmt.Errorf("%s", msg)
		}
	}
	if err != nil {
		return fmt.Errorf("%s: %w", op, err)
	}
	return nil
}
