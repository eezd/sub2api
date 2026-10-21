import { apiClient } from './client'

export type WorkbenchProtocol = 'images' | 'gemini'
export type JobStatus = 'running' | 'complete' | 'partial' | 'failed' | 'cancelled' | 'interrupted'
export interface WorkbenchInput {
  request_id: string
  api_key_id: number
  protocol: WorkbenchProtocol
  model: string
  prompt: string
  count: number
  size: string
  quality: string
  aspect_ratio: string
  image_size: string
  reference_ids: string[]
}
export interface WorkbenchAsset { id: string; filename: string; mime_type: string; size: number; width: number; height: number; draft: boolean }
export interface WorkbenchItem { position: number; status: 'pending' | 'complete' | 'failed' | 'cancelled' | 'interrupted'; asset_id: string | null; error: string; duration_ms: number }
export interface WorkbenchJob { id: string; api_key_id: number | null; input: WorkbenchInput; status: JobStatus; deleting: boolean; created_at: string; updated_at: string; items: WorkbenchItem[]; assets: WorkbenchAsset[] }
export interface WorkbenchLimits { used_bytes: number; reserved_bytes: number; storage_bytes: number; file_bytes: number; request_bytes: number }
export interface WorkbenchModels { ids: string[]; protocol: WorkbenchProtocol; native_gemini_available: boolean }
const root = '/image-workbench'

export const imageWorkbenchAPI = {
  async limits() { return (await apiClient.get<WorkbenchLimits>(`${root}/limits`)).data },
  async models(key: number, protocol?: WorkbenchProtocol, signal?: AbortSignal) { return (await apiClient.get<WorkbenchModels>(`${root}/models`, { params: { api_key_id: key, protocol }, signal })).data },
  async list(page = 1) { return (await apiClient.get<WorkbenchJob[]>(`${root}/tasks`, { params: { page } })).data },
  async get(id: string) { return (await apiClient.get<WorkbenchJob>(`${root}/tasks/${id}`)).data },
  async submit(input: WorkbenchInput) { return (await apiClient.post<{ job: WorkbenchJob; existing: boolean }>(`${root}/tasks`, input)).data },
  async cancel(id: string) { await apiClient.post(`${root}/tasks/${id}/cancel`) },
  async remove(id: string) { await apiClient.delete(`${root}/tasks/${id}`) },
  async drafts() { return (await apiClient.get<WorkbenchAsset[]>(`${root}/assets`)).data },
  async upload(file: File) {
    const data = new FormData(); data.append('file', file)
    return (await apiClient.post<WorkbenchAsset>(`${root}/assets`, data, { headers: { 'Content-Type': undefined }, timeout: 120_000 })).data
  },
  async download(id: string) { return (await apiClient.get<Blob>(`${root}/assets/${id}`, { responseType: 'blob', timeout: 120_000 })).data },
  async removeAsset(id: string) { await apiClient.delete(`${root}/assets/${id}`) }
}
