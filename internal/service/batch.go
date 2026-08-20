package service

import (
	"context"

	"metalmics/internal/domain"
)

// BatchAcceptItem 是批量接收的单项结果。
type BatchAcceptItem struct {
	LotID int64  `json:"lot_id"`
	LotNo string `json:"lot_no,omitempty"`
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
	Code  string `json:"code,omitempty"`
}

// BatchAcceptResult 是批量接收的汇总结果，支持部分失败。
type BatchAcceptResult struct {
	Succeeded []BatchAcceptItem `json:"succeeded"`
	Failed    []BatchAcceptItem `json:"failed"`
}

// BatchAccept 批量接收确认：每个批次在独立事务中执行接收，
// 单项失败不影响其他项（部分失败语义），结果按输入顺序返回。
func (s *DailyService) BatchAccept(ctx context.Context, lotIDs []int64, actor string) (*BatchAcceptResult, error) {
	if len(lotIDs) == 0 {
		return nil, domain.Validation("lot_ids", "批次列表不能为空")
	}
	if len(lotIDs) > 100 {
		return nil, domain.Validation("lot_ids", "单次批量接收不能超过 100 个批次")
	}
	result := &BatchAcceptResult{Succeeded: []BatchAcceptItem{}, Failed: []BatchAcceptItem{}}
	for _, id := range lotIDs {
		lot, err := s.AcceptLot(ctx, id, 0, actor)
		if err != nil {
			item := BatchAcceptItem{LotID: id, OK: false, Error: err.Error()}
			if de, ok := err.(*domain.Error); ok {
				item.Error = de.Message
				item.Code = string(de.Code)
			}
			result.Failed = append(result.Failed, item)
			continue
		}
		result.Succeeded = append(result.Succeeded, BatchAcceptItem{LotID: id, LotNo: lot.LotNo, OK: true})
	}
	return result, nil
}
