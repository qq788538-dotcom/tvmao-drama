package http

import (
	"context"
	"errors"
	nethttp "net/http"

	"github.com/go-chi/chi/v5"

	"github.com/qq788538-dotcom/tvmao-drama/server/internal/objectstore"
)

// ObjectStoreProber：*objectstore.Publisher 满足。
type ObjectStoreProber interface {
	Probe(ctx context.Context, c objectstore.Config) objectstore.ProbeResult
}

func init() {
	settingsRouteRegistrars = append(settingsRouteRegistrars, registerObjectStorageSettings)
}

func registerObjectStorageSettings(r chi.Router, d SettingsCenterDeps) {
	if d.Settings == nil {
		return
	}
	h := &objectStorageHandler{d: d}
	r.Get("/object-storage", h.get)
	r.Put("/object-storage", h.put)
	r.Delete("/object-storage", h.clear)
	if d.ObjectStore != nil {
		r.Post("/object-storage/test", h.test)
	}
}

type objectStorageHandler struct{ d SettingsCenterDeps }

type objectStorageView struct {
	Configured      bool   `json:"configured"`
	Provider        string `json:"provider"`
	Endpoint        string `json:"endpoint"`
	Region          string `json:"region"`
	Bucket          string `json:"bucket"`
	Prefix          string `json:"prefix"`
	AccessKey       string `json:"accessKey"`
	SecretKeySet    bool   `json:"secretKeySet"`
	SecretKeyMasked string `json:"secretKeyMasked"`
}

func (h *objectStorageHandler) writeView(w nethttp.ResponseWriter) {
	c, ok, err := objectstore.Load(h.d.Settings)
	if err != nil {
		scWriteError(w, err)
		return
	}
	writeJSON(w, nethttp.StatusOK, objectStorageView{
		Configured: ok, Provider: c.Provider, Endpoint: c.Endpoint, Region: c.Region,
		Bucket: c.Bucket, Prefix: c.Prefix, AccessKey: c.AccessKey,
		SecretKeySet: c.SecretKey != "", SecretKeyMasked: maskAPIKey(c.SecretKey),
	})
}

func (h *objectStorageHandler) get(w nethttp.ResponseWriter, _ *nethttp.Request) { h.writeView(w) }

// decode 解析请求体；secretKey 留空时沿用已保存的值（首次保存由 Normalize 报 secretKey 必填）。
func (h *objectStorageHandler) decode(w nethttp.ResponseWriter, r *nethttp.Request) (objectstore.Config, error) {
	var c objectstore.Config
	if err := scDecodeStrict(w, r, &c); err != nil {
		return c, err
	}
	if c.SecretKey == "" {
		if saved, ok, err := objectstore.Load(h.d.Settings); err != nil {
			return c, err
		} else if ok {
			c.SecretKey = saved.SecretKey
		}
	}
	return c, nil
}

func toValidation(err error) error {
	var fe *objectstore.FieldError
	if errors.As(err, &fe) {
		return scValidation(fe.Field, fe.Msg)
	}
	return err
}

func (h *objectStorageHandler) put(w nethttp.ResponseWriter, r *nethttp.Request) {
	c, err := h.decode(w, r)
	if err != nil {
		scWriteError(w, err)
		return
	}
	if err := objectstore.Save(h.d.Settings, c); err != nil {
		scWriteError(w, toValidation(err))
		return
	}
	h.writeView(w)
}

func (h *objectStorageHandler) clear(w nethttp.ResponseWriter, _ *nethttp.Request) {
	if err := objectstore.Clear(h.d.Settings); err != nil {
		scWriteError(w, err)
		return
	}
	h.writeView(w)
}

func (h *objectStorageHandler) test(w nethttp.ResponseWriter, r *nethttp.Request) {
	c, err := h.decode(w, r)
	if err != nil {
		scWriteError(w, err)
		return
	}
	writeJSON(w, nethttp.StatusOK, h.d.ObjectStore.Probe(r.Context(), c))
}
