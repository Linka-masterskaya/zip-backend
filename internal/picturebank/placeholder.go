package picturebank

const deletedPictureSVG = `<svg xmlns="http://www.w3.org/2000/svg" width="640" height="480" viewBox="0 0 640 480" role="img" aria-label="Картинка удалена">
<rect width="640" height="480" rx="24" fill="#F3F4F6"/>
<path d="M214 154h212a18 18 0 0 1 18 18v136a18 18 0 0 1-18 18H214a18 18 0 0 1-18-18V172a18 18 0 0 1 18-18Z" fill="#FFF" stroke="#9CA3AF" stroke-width="8"/>
<circle cx="266" cy="210" r="22" fill="#D1D5DB"/>
<path d="m212 300 72-72 54 54 34-34 56 52" fill="none" stroke="#9CA3AF" stroke-width="12" stroke-linecap="round" stroke-linejoin="round"/>
<path d="m230 134 180 212" stroke="#EF4444" stroke-width="16" stroke-linecap="round"/>
<text x="320" y="390" text-anchor="middle" font-family="Arial, sans-serif" font-size="28" fill="#4B5563">Картинка удалена</text>
</svg>`

const unavailablePictureSVG = `<svg xmlns="http://www.w3.org/2000/svg" width="640" height="480" viewBox="0 0 640 480" role="img" aria-label="Картинка недоступна">
<rect width="640" height="480" rx="24" fill="#F3F4F6"/>
<path d="M214 154h212a18 18 0 0 1 18 18v136a18 18 0 0 1-18 18H214a18 18 0 0 1-18-18V172a18 18 0 0 1 18-18Z" fill="#FFF" stroke="#9CA3AF" stroke-width="8"/>
<circle cx="266" cy="210" r="22" fill="#D1D5DB"/>
<path d="m212 300 72-72 54 54 34-34 56 52" fill="none" stroke="#9CA3AF" stroke-width="12" stroke-linecap="round" stroke-linejoin="round"/>
<path d="M320 184v82" stroke="#F59E0B" stroke-width="16" stroke-linecap="round"/>
<circle cx="320" cy="300" r="9" fill="#F59E0B"/>
<text x="320" y="390" text-anchor="middle" font-family="Arial, sans-serif" font-size="28" fill="#4B5563">Картинка недоступна</text>
</svg>`

// DeletedPicturePlaceholder is returned when a referenced external picture was removed.
func DeletedPicturePlaceholder() *Image {
	return &Image{Data: []byte(deletedPictureSVG), ContentType: "image/svg+xml"}
}

// UnavailablePicturePlaceholder avoids broken image UI during a transient upstream outage.
func UnavailablePicturePlaceholder() *Image {
	return &Image{Data: []byte(unavailablePictureSVG), ContentType: "image/svg+xml"}
}
