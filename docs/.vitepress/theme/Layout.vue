<script setup lang="ts">
import DefaultTheme from 'vitepress/theme'

import SiteFooter from './components/SiteFooter.vue'

const { Layout } = DefaultTheme
</script>

<template>
  <Layout>
    <!-- The wordmark of the Admin's navbar: a terracotta v, then latte. -->
    <template #nav-bar-title-after>
      <span class="vl-wordmark"><span>v</span>latte</span>
    </template>
    <template #layout-bottom>
      <SiteFooter />
    </template>
  </Layout>
</template>
