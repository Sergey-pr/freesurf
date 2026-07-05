import { createApp } from 'vue'
import { createPinia } from 'pinia'
import { polyfillCountryFlagEmojis } from 'country-flag-emoji-polyfill'
import flagFontUrl from './assets/fonts/TwemojiCountryFlags.woff2?url'
import App from './App.vue'
import './assets/main.css'

// Windows ships no flag-emoji glyphs, so node names ending in a flag render as
// tofu/letter-codes. This bundles a flags-only webfont and applies it only on
// browsers that fail the flag-support test (i.e. Windows); macOS keeps its
// native Apple flags. The font must be bundled locally, not CDN-loaded, so it
// works offline/pre-connection.
polyfillCountryFlagEmojis('Twemoji Country Flags', flagFontUrl)

const app = createApp(App)
app.use(createPinia())
app.mount('#app')
