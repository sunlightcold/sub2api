import { defineComponent } from 'vue'
import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'

const { estimateCleanupMock, listCleanupTasksMock, createCleanupTaskMock, showErrorMock } = vi.hoisted(() => ({
  estimateCleanupMock: vi.fn(),
  listCleanupTasksMock: vi.fn(),
  createCleanupTaskMock: vi.fn(),
  showErrorMock: vi.fn(),
}))

vi.mock('@/api/admin/usage', () => {
  const adminUsageAPI = {
    estimateCleanup: estimateCleanupMock,
    listCleanupTasks: listCleanupTasksMock,
    createCleanupTask: createCleanupTaskMock,
    cancelCleanupTask: vi.fn(),
  }
  return { adminUsageAPI, default: adminUsageAPI }
})

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({
    showError: showErrorMock,
    showSuccess: vi.fn(),
  }),
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string, params?: Record<string, unknown>) => params?.count ? `${key}:${params.count}` : key,
    }),
  }
})

import UsageCleanupDialog from '../UsageCleanupDialog.vue'

const BaseDialogStub = defineComponent({
  name: 'BaseDialog',
  props: { show: Boolean },
  template: '<div v-if="show"><slot /><slot name="footer" /></div>',
})

const DateRangePickerStub = defineComponent({
  name: 'DateRangePicker',
  props: { startDate: String, endDate: String },
  emits: ['update:startDate', 'update:endDate'],
  template: `
    <button
      data-testid="change-date-range"
      @click="$emit('update:startDate', '2026-07-01'); $emit('update:endDate', '2026-07-03')"
    >change dates</button>
  `,
})

const UsageFiltersStub = defineComponent({
  name: 'UsageFilters',
  props: { modelValue: { type: Object, required: true }, startDate: String, endDate: String },
  emits: ['update:modelValue'],
  template: `
    <button
      data-testid="change-cleanup-filter"
      @click="$emit('update:modelValue', { ...modelValue, model: 'gpt-5' })"
    >change filter</button>
  `,
})

const ConfirmDialogStub = defineComponent({
  name: 'ConfirmDialog',
  props: { show: Boolean, message: String },
  template: '<div v-if="show" data-testid="confirm-message">{{ message }}</div>',
})

const mountDialog = async () => {
  const wrapper = mount(UsageCleanupDialog, {
    props: {
      show: false,
      filters: { user_id: 7, model: 'gpt-4', billing_mode: 'image' },
      startDate: '2026-06-01',
      endDate: '2026-06-02',
    },
    global: {
      stubs: {
        BaseDialog: BaseDialogStub,
        DateRangePicker: DateRangePickerStub,
        UsageFilters: UsageFiltersStub,
        ConfirmDialog: ConfirmDialogStub,
        Pagination: true,
      },
    },
  })
  await wrapper.setProps({ show: true })
  await flushPromises()
  return wrapper
}

describe('UsageCleanupDialog', () => {
  beforeEach(() => {
    estimateCleanupMock.mockReset().mockResolvedValue({ count: 1234 })
    listCleanupTasksMock.mockReset().mockResolvedValue({ items: [], total: 0, page: 1, page_size: 5 })
    createCleanupTaskMock.mockReset().mockResolvedValue({})
    showErrorMock.mockReset()
  })

  it('estimates with the active date range and filters', async () => {
    const wrapper = await mountDialog()

    await wrapper.get('[data-testid="change-date-range"]').trigger('click')
    await wrapper.get('[data-testid="cleanup-estimate-button"]').trigger('click')
    await flushPromises()

    expect(estimateCleanupMock).toHaveBeenCalledWith(expect.objectContaining({
      start_date: '2026-07-01',
      end_date: '2026-07-03',
      user_id: 7,
      model: 'gpt-4',
      billing_mode: 'image',
    }))
    expect(wrapper.get('[data-testid="cleanup-estimate-count"]').text()).toContain('1,234')
    wrapper.unmount()
  })

  it('invalidates a displayed estimate when filters change', async () => {
    const wrapper = await mountDialog()

    await wrapper.get('[data-testid="cleanup-estimate-button"]').trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-testid="cleanup-estimate-count"]').exists()).toBe(true)

    await wrapper.get('[data-testid="change-cleanup-filter"]').trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-testid="cleanup-estimate-count"]').exists()).toBe(false)
    wrapper.unmount()
  })

  it('includes the estimate in the confirmation message', async () => {
    const wrapper = await mountDialog()

    await wrapper.get('[data-testid="cleanup-estimate-button"]').trigger('click')
    await flushPromises()
    const submit = wrapper.findAll('button').find((button) => button.text() === 'admin.usage.cleanup.submit')
    expect(submit).toBeDefined()
    await submit?.trigger('click')

    expect(wrapper.get('[data-testid="confirm-message"]').text()).toContain('1,234')
    wrapper.unmount()
  })
})
