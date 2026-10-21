import { getDocument, GlobalWorkerOptions } from 'pdfjs-dist'
import workerURL from 'pdfjs-dist/build/pdf.worker.min.mjs?url'
import { AttachmentError, byteLength, TEXT_LIMIT } from './chatAttachments'

GlobalWorkerOptions.workerSrc = workerURL

export async function extractPDF(file: File): Promise<string> {
  const task = getDocument({ data: new Uint8Array(await file.arrayBuffer()), isEvalSupported: false, useWorkerFetch: false })
  try {
    const pdf = await task.promise
    if (pdf.numPages > 200) throw new AttachmentError('pdfPages')
    const pages: string[] = []; let size = 0
    for (let n = 1; n <= pdf.numPages; n++) {
      const page = await pdf.getPage(n)
      const content = await page.getTextContent()
      const text = content.items.map(item => 'str' in item ? item.str + (item.hasEOL ? '\n' : ' ') : '').join('')
      size += byteLength(text) + 1
      if (size > TEXT_LIMIT) throw new AttachmentError('textSize')
      pages.push(text); page.cleanup()
    }
    const text = pages.join('\n')
    if (!text.trim()) throw new AttachmentError('pdfEmpty')
    return text
  } catch (error) {
    if (error instanceof AttachmentError) throw error
    throw new AttachmentError('pdfInvalid')
  } finally { await task.destroy() }
}
