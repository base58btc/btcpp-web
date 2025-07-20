package types


type (

	CardDimens struct {
		Height float64
		Width  float64
	}
)

var MediaCards = []string{ "social", "1080p", "insta" }

/* Heights of the media images, in inches */
/* Use this to convert https://www.unitconverters.net/typography/pixel-x-to-inch.htm */
var MediaDimens = map[string]CardDimens{
	/* 314x600px */
	"social": CardDimens{
		Height: float64(3.27),
		Width:  float64(6.25),
	},
	/* 1080x1920px */
	"1080p": CardDimens{
		Height: float64(11.25),
		Width:  float64(20),
	},
	/* 1080x1080px */
	"insta": CardDimens{
		Height: float64(11.25),
		Width:  float64(11.25),
	},
}

