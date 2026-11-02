import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import EvaluationSvgPreview from '../EvaluationSvgPreview.vue'

describe('EvaluationSvgPreview', () => {
  const createURL = vi.fn(() => 'blob:pelican-preview')
  const revokeURL = vi.fn()
  let contents: string

  beforeEach(() => {
    contents = ''
    createURL.mockClear()
    revokeURL.mockClear()
    vi.stubGlobal('Blob', class {
      constructor(parts: string[]) { contents = parts.join('') }
    })
    vi.stubGlobal('URL', { createObjectURL: createURL, revokeObjectURL: revokeURL })
  })
  afterEach(() => vi.unstubAllGlobals())

  it('uses an inert image resource and strips executable/external SVG content', () => {
    const wrapper = mount(EvaluationSvgPreview, { props: {
      alt: 'Pelican',
      svg: '<svg xmlns="http://www.w3.org/2000/svg" onload="alert(1)"><script>alert(2)</script><foreignObject><div>HTML</div></foreignObject><image href="https://example.com/x"/><use href="https://example.com/s.svg#x"/><circle cx="10" cy="10" r="5"/></svg>'
    } })
    expect(wrapper.get('img').attributes('src')).toBe('blob:pelican-preview')
    expect(wrapper.find('svg').exists()).toBe(false)
    expect(contents).toContain('<circle')
    expect(contents).not.toMatch(/onload|script|foreignObject|https:\/\/example\.com/)
    wrapper.unmount()
    expect(revokeURL).toHaveBeenCalledWith('blob:pelican-preview')
  })

  it('does not render malformed or non-SVG output', () => {
    const wrapper = mount(EvaluationSvgPreview, { props: { alt: 'Pelican', svg: 'not a drawing' } })
    expect(wrapper.find('img').exists()).toBe(false)
    expect(createURL).not.toHaveBeenCalled()
    wrapper.unmount()
  })
})
