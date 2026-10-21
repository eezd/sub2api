export default {
  imageWorkbench: {
    loading: 'Loading…',
    title: 'Image Workbench', intro: 'Create images using your own API key, prompts, and reference images.',
    refreshModels: 'Refresh models', unusedDrafts: 'Unused uploads (count toward storage)', add: 'Add', deleteDraft: 'Delete',
    key: 'API key', selectKey: 'Select your API key', protocol: 'API protocol', model: 'Model', selectModel: 'Select a model',
    modelHint: 'A listed model may not support image generation. Available features and parameters depend on your group.',
    prompt: 'Prompt', promptPlaceholder: 'Describe your image, up to 8000 characters', size: 'Size', quality: 'Quality', ratio: 'Aspect ratio', resolution: 'Resolution',
    auto: 'Auto', low: 'Low', medium: 'Medium', high: 'High', count: 'Image count',
    references: 'References', upload: 'Upload images', referenceHint: 'Upload, drop, or paste PNG / JPEG / WebP images here; up to 20 MiB each and 5 images.',
    removeReference: 'Remove reference', referenceCount: 'Up to 5 reference images. Remove an image first.', fileError: 'Choose a nonempty PNG, JPEG, or WebP image no larger than 20 MiB.',
    storage: 'Image storage {used} / 100 MB, {reserved} MB reserved (separate from Chat)',
    reserveHint: 'This task reserves up to {size} MB; actual sizes are counted after completion. Remove history or references if storage is full.',
    generate: 'Generate', processing: 'Processing…', running: 'Generating in background', waiting: 'Waiting for image…',
    billingNotice: 'Your selected key’s normal group pricing applies. Tasks continue after leaving the page. Failures are not retried automatically; cancellation may still incur charges.',
    results: 'Results', empty: 'Enter a prompt and select a model to start creating.', resultAlt: 'Generated image {n}', download: 'Download', useReference: 'Use as reference', reuse: 'Reuse settings',
    cancel: 'Cancel task', cancelling: 'Cancellation requested. Wait for the task to stop; upstream charges may already apply.',
    history: 'History', noHistory: 'No generation history', more: 'Load more', delete: 'Delete record and unshared images', retryDelete: 'Retry deletion',
    deleteConfirm: 'Delete this task and images used only by it? Images referenced by other tasks will remain.', close: 'Close', preview: 'Image preview',
    checkSubmission: 'Check this submission (no duplicate generation)', networkError: 'Request failed. Check your connection and try again.',
    status: { running: 'Generating', pending: 'Pending', complete: 'Complete', partial: 'Partial success', failed: 'Failed', cancelled: 'Cancelled', interrupted: 'Interrupted' }
  }
}
