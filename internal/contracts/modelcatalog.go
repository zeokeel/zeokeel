package contracts

import cordis "github.com/zeokeel/zeokeel-kernel"

// ModelInfo is one selectable model preset in the composer model menu.
type ModelInfo struct {
	// ID is the model identifier sent to the provider (chat.model).
	ID string
	// Name is the display label.
	Name string
	// Note is a short hint (capability or provider).
	Note string
}

// ModelCatalogService lists the model presets offered by the product.
type ModelCatalogService interface {
	Models() []ModelInfo
}

// KeyModelCatalog is provided by the model catalog backend module.
var KeyModelCatalog = cordis.NewServiceKey[ModelCatalogService]("zeokeel/modelcatalog")
