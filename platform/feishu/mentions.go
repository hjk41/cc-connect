package feishu

import (
	"github.com/chenhg5/cc-connect/core"

	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

// coreMentions converts Feishu mention metadata for engine commands such as
// `/config set allow_from @user`. The bot's own mention is not a grant target.
func (p *Platform) coreMentions(mentions []*larkim.MentionEvent) []core.Mention {
	botOpenID := p.getBotOpenID()
	out := make([]core.Mention, 0, len(mentions))
	for _, mention := range mentions {
		if mention == nil || mention.Id == nil || mention.Id.OpenId == nil {
			continue
		}
		userID := *mention.Id.OpenId
		if userID == "" || (botOpenID != "" && userID == botOpenID) {
			continue
		}
		name := ""
		if mention.Name != nil {
			name = *mention.Name
		}
		out = append(out, core.Mention{UserID: userID, Name: name})
	}
	return out
}
