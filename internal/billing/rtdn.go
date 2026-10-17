package billing

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"go.uber.org/zap"
	"google.golang.org/api/idtoken"
)

const maxRTDNBodySize = 1 << 20

// ErrRTDNPermanent marks a notification that retrying cannot apply, so the
// handler acknowledges it instead of asking Pub/Sub to redeliver.
var ErrRTDNPermanent = errors.New("rtdn notification cannot be applied")

// TokenValidator verifies a Google-signed ID token for audience and returns its payload.
type TokenValidator func(ctx context.Context, token, audience string) (*idtoken.Payload, error)

type pushEnvelope struct {
	Message struct {
		Data      string `json:"data"`
		MessageID string `json:"messageId"`
	} `json:"message"`
}

type developerNotification struct {
	PackageName              string `json:"packageName"`
	SubscriptionNotification *struct {
		PurchaseToken string `json:"purchaseToken"`
	} `json:"subscriptionNotification"`
	VoidedPurchaseNotification *struct {
		PurchaseToken string `json:"purchaseToken"`
	} `json:"voidedPurchaseNotification"`
	TestNotification *json.RawMessage `json:"testNotification"`
}

// NewRTDNHandler returns the HTTP handler for Google Play real-time developer
// notifications delivered by a Pub/Sub push subscription. Requests must carry
// an ID token for audience issued to serviceAccount. A nil sync, used when
// Google Play billing is not configured, makes the handler answer 503.
func NewRTDNHandler(log *zap.SugaredLogger, sync func(ctx context.Context, token string, voided bool) error, validate TokenValidator, audience, serviceAccount, packageName string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if sync == nil {
			http.Error(w, "google play billing not configured", http.StatusServiceUnavailable)
			return
		}
		if err := authorizeRTDN(r, validate, audience, serviceAccount); err != nil {
			log.Warnw("rtdn unauthorized", zap.Error(err))
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxRTDNBodySize))
		if err != nil {
			log.Warnw("rtdn body unreadable", zap.Error(err))
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		note, err := decodeRTDN(body)
		if err != nil {
			log.Warnw("rtdn malformed", zap.Error(err))
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if note.PackageName != packageName {
			log.Warnw("rtdn for another package dropped", "package_name", note.PackageName)
			w.WriteHeader(http.StatusNoContent)
			return
		}

		var token string
		var voided bool
		switch {
		case note.TestNotification != nil:
			log.Infow("rtdn test notification received")
			w.WriteHeader(http.StatusNoContent)
			return
		case note.SubscriptionNotification != nil:
			token = note.SubscriptionNotification.PurchaseToken
		case note.VoidedPurchaseNotification != nil:
			token = note.VoidedPurchaseNotification.PurchaseToken
			voided = true
		default:
			log.Infow("rtdn notification ignored")
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if token == "" {
			log.Warnw("rtdn notification without purchase token dropped", "voided", voided)
			w.WriteHeader(http.StatusNoContent)
			return
		}

		err = sync(r.Context(), token, voided)
		switch {
		case errors.Is(err, ErrRTDNPermanent):
			log.Warnw("rtdn notification dropped", "voided", voided, zap.Error(err))
		case err != nil:
			log.Errorw("rtdn sync failed", "voided", voided, zap.Error(err))
			http.Error(w, "sync failed", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}

func authorizeRTDN(r *http.Request, validate TokenValidator, audience, serviceAccount string) error {
	token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok || token == "" {
		return errors.New("missing bearer token")
	}
	payload, err := validate(r.Context(), token, audience)
	if err != nil {
		return err
	}
	if email, _ := payload.Claims["email"].(string); email != serviceAccount {
		return errors.New("token email is not the push service account")
	}
	if verified, _ := payload.Claims["email_verified"].(bool); !verified {
		return errors.New("token email is not verified")
	}
	return nil
}

func decodeRTDN(body []byte) (*developerNotification, error) {
	var env pushEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, err
	}
	data, err := base64.StdEncoding.DecodeString(env.Message.Data)
	if err != nil {
		return nil, err
	}
	var note developerNotification
	if err := json.Unmarshal(data, &note); err != nil {
		return nil, err
	}
	return &note, nil
}
