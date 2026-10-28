import DefaultTheme from 'vitepress/theme'
import type { Theme } from 'vitepress'

import '@fontsource/sora/400.css'
import '@fontsource/sora/600.css'
import '@fontsource/sora/700.css'
import '@fontsource/jetbrains-mono/400.css'
import '@fontsource/jetbrains-mono/500.css'

import Layout from './Layout.vue'
import './vars.css'
import './home.css'

export default {
  extends: DefaultTheme,
  Layout,
} satisfies Theme
