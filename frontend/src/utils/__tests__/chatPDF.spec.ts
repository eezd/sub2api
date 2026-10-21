import { beforeEach, describe, expect, it, vi } from 'vitest'
import { extractPDF } from '../chatPDF'

const mocks = vi.hoisted(() => ({ getDocument: vi.fn(), destroy: vi.fn(), getPage: vi.fn() }))
vi.mock('pdfjs-dist', () => ({ GlobalWorkerOptions: {}, getDocument: mocks.getDocument }))
const file = { arrayBuffer: async () => new ArrayBuffer(8) } as File
beforeEach(() => {
  vi.clearAllMocks()
  mocks.destroy.mockResolvedValue(undefined)
  mocks.getPage.mockResolvedValue({ getTextContent: async () => ({ items: [{ str: '测试文字', hasEOL: true }] }), cleanup: vi.fn() })
  mocks.getDocument.mockReturnValue({ promise: Promise.resolve({ numPages: 1, getPage: mocks.getPage }), destroy: mocks.destroy })
})
describe('PDF extraction constraints', () => {
  it('extracts text without rendering pages and always disposes the worker task', async () => {
    expect(await extractPDF(file)).toContain('测试文字')
    expect(mocks.destroy).toHaveBeenCalledTimes(1)
  })
  it('rejects PDFs above 200 pages before reading them', async () => {
    mocks.getDocument.mockReturnValue({ promise: Promise.resolve({ numPages: 201, getPage: mocks.getPage }), destroy: mocks.destroy })
    await expect(extractPDF(file)).rejects.toMatchObject({ code: 'pdfPages' })
    expect(mocks.getPage).not.toHaveBeenCalled(); expect(mocks.destroy).toHaveBeenCalledTimes(1)
  })
  it('rejects over 2 MB of extracted UTF-8 rather than silently truncating', async () => {
    mocks.getPage.mockResolvedValue({ getTextContent: async () => ({ items: [{ str: '文'.repeat(700000) }] }), cleanup: vi.fn() })
    await expect(extractPDF(file)).rejects.toMatchObject({ code: 'textSize' })
    expect(mocks.destroy).toHaveBeenCalledTimes(1)
  })
  it('rejects scans and password protected or corrupt files', async () => {
    mocks.getPage.mockResolvedValue({ getTextContent: async () => ({ items: [] }), cleanup: vi.fn() })
    await expect(extractPDF(file)).rejects.toMatchObject({ code: 'pdfEmpty' })
    mocks.getDocument.mockReturnValue({ promise: Promise.reject(new Error('PasswordException')), destroy: mocks.destroy })
    await expect(extractPDF(file)).rejects.toMatchObject({ code: 'pdfInvalid' })
    expect(mocks.destroy).toHaveBeenCalledTimes(2)
  })
})
