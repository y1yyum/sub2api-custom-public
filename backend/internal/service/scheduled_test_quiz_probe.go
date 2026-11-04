package service

import "context"

// 智力测试只管理自己的限制，不使用连通性探测的永久错误开关。
type scheduledQuizAccountRepository struct{ AccountRepository }

func (scheduledQuizAccountRepository) SetError(context.Context, int64, string) error { return nil }
func (scheduledQuizAccountRepository) ClearError(context.Context, int64) error       { return nil }

func (s *AccountTestService) forScheduledQuiz() *AccountTestService {
	antigravityGateway := s.antigravityGatewayService
	if antigravityGateway != nil {
		quizGateway := *antigravityGateway
		quizGateway.accountRepo = scheduledQuizAccountRepository{AccountRepository: antigravityGateway.accountRepo}
		antigravityGateway = &quizGateway
	}
	return &AccountTestService{
		accountRepo:               scheduledQuizAccountRepository{AccountRepository: s.accountRepo},
		geminiTokenProvider:       s.geminiTokenProvider,
		claudeTokenProvider:       s.claudeTokenProvider,
		grokTokenProvider:         s.grokTokenProvider,
		antigravityGatewayService: antigravityGateway,
		httpUpstream:              s.httpUpstream,
		cfg:                       s.cfg,
		settingService:            s.settingService,
		tlsFPProfileService:       s.tlsFPProfileService,
		pluginManager:             s.pluginManager,
		openaiGatewayService:      s.openaiGatewayService,
		agentIdentityWS:           s.agentIdentityWS,
		grokWSDialer:              s.grokWSDialer,
	}
}
