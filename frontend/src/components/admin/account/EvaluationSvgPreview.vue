<template>
  <img
    v-if="imageURL"
    :src="imageURL"
    :alt="alt"
    loading="lazy"
    decoding="async"
    class="max-h-96 w-full rounded-lg border border-gray-200 bg-white object-contain dark:border-dark-600"
  />
</template>

<script setup lang="ts">
import { onUnmounted, ref, watch } from 'vue'
import DOMPurify from 'dompurify'

const props = defineProps<{ svg: string; alt: string }>()
const imageURL = ref('')

function releaseImage() {
  if (imageURL.value) URL.revokeObjectURL(imageURL.value)
  imageURL.value = ''
}

watch(() => props.svg, (svg) => {
  releaseImage()
  if (!svg) return
  const sanitized = DOMPurify.sanitize(svg, {
    USE_PROFILES: { svg: true, svgFilters: true },
    ADD_TAGS: ['animate', 'animateTransform', 'animateMotion', 'mpath'],
    FORBID_TAGS: ['foreignObject', 'script', 'image', 'a'],
  })
  const document = new DOMParser().parseFromString(sanitized, 'image/svg+xml')
  if (document.querySelector('parsererror') || document.documentElement.localName !== 'svg') return
  for (const element of document.querySelectorAll('*')) {
    for (const attribute of Array.from(element.attributes)) {
      if (attribute.localName === 'href' && !attribute.value.startsWith('#')) {
        element.removeAttributeNode(attribute)
      }
    }
  }
  document.documentElement.setAttribute('xmlns', 'http://www.w3.org/2000/svg')
  // SVG is an image resource, never executable markup in the admin page.
  imageURL.value = URL.createObjectURL(new Blob(
    [new XMLSerializer().serializeToString(document.documentElement)],
    { type: 'image/svg+xml' }
  ))
}, { immediate: true })

onUnmounted(releaseImage)
</script>
