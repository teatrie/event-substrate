export class InsufficientCreditsError extends Error {
  constructor(message = 'Insufficient credits') {
    super(message)
    this.name = 'InsufficientCreditsError'
  }
}

export async function requestUploadUrl(apiGatewayUrl, token, fileMetadata) {
  const base = apiGatewayUrl.replace(/\/+$/, '')
  const response = await fetch(`${base}/api/v1/media/upload-url`, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
      Authorization: `Bearer ${token}`,
    },
    body: JSON.stringify(fileMetadata),
  })
  if (!response.ok) {
    if (response.status === 402) {
      throw new InsufficientCreditsError()
    }
    throw new Error(`Request failed with status ${response.status}`)
  }
  return response.json()
}

export async function uploadFileToStorage(uploadUrl, file) {
  const response = await fetch(uploadUrl, {
    method: 'PUT',
    headers: { 'Content-Type': file.type },
    body: file,
  })
  if (!response.ok) {
    throw new Error(`Upload failed with status ${response.status}`)
  }
  return { success: true }
}

export async function fetchUserFiles(supabaseClient) {
  if (!supabaseClient) {
    throw new Error('Supabase client is required')
  }
  const { data, error } = await supabaseClient
    .from('media_files')
    .select('id, file_name, file_path, file_size, media_type, upload_time')
    .order('created_at', { ascending: false })
  if (error) {
    throw new Error(error.message)
  }
  return data || []
}

export function formatFileSize(bytes) {
  if (bytes <= 0) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  const i = Math.floor(Math.log(bytes) / Math.log(1024))
  const value = bytes / 1024 ** i
  return `${parseFloat(value.toFixed(1))} ${units[i]}`
}

const ALLOWED_MEDIA_TYPES = new Set([
  'image/jpeg', 'image/png', 'image/gif', 'image/webp',
  'video/mp4', 'video/webm',
  'audio/mpeg', 'audio/wav', 'audio/ogg',
])

export function isAllowedMediaType(mediaType) {
  return ALLOWED_MEDIA_TYPES.has(mediaType)
}

const MEDIA_CATEGORIES = new Set(['image', 'video', 'audio'])

export function getMediaCategory(mediaType) {
  if (!mediaType) return 'unknown'
  const prefix = mediaType.split('/')[0]
  return MEDIA_CATEGORIES.has(prefix) ? prefix : 'unknown'
}

export async function requestDownloadUrl(apiGatewayUrl, token, filePath) {
  const base = apiGatewayUrl.replace(/\/+$/, '')
  const response = await fetch(`${base}/api/v1/media/download-url`, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
      Authorization: `Bearer ${token}`,
    },
    body: JSON.stringify({ file_path: filePath }),
  })
  if (!response.ok) {
    throw new Error(`Request failed with status ${response.status}`)
  }
  return response.json()
}

export async function deleteFile(apiGatewayUrl, token, filePath) {
  const base = apiGatewayUrl.replace(/\/+$/, '')
  const response = await fetch(`${base}/api/v1/media/delete`, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
      Authorization: `Bearer ${token}`,
    },
    body: JSON.stringify({ file_path: filePath }),
  })
  if (!response.ok) {
    throw new Error(`Request failed with status ${response.status}`)
  }
  return response.json()
}

export async function requestUploadIntent(apiGatewayUrl, token, fileMetadata) {
  const base = apiGatewayUrl.replace(/\/+$/, '')
  const response = await fetch(`${base}/api/v1/media/upload-intent`, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
      Authorization: `Bearer ${token}`,
    },
    body: JSON.stringify(fileMetadata),
  })
  if (response.status === 402) {
    throw new InsufficientCreditsError()
  }
  if (response.status !== 202) {
    throw new Error(`Request failed with status ${response.status}`)
  }
  return response.json()
}

export async function requestDownloadIntent(apiGatewayUrl, token, filePath) {
  const base = apiGatewayUrl.replace(/\/+$/, '')
  const response = await fetch(`${base}/api/v1/media/download-intent`, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
      Authorization: `Bearer ${token}`,
    },
    body: JSON.stringify({ file_path: filePath }),
  })
  if (response.status !== 202) {
    throw new Error(`Request failed with status ${response.status}`)
  }
  return response.json()
}

export async function requestDeleteIntent(apiGatewayUrl, token, filePath) {
  const base = apiGatewayUrl.replace(/\/+$/, '')
  const response = await fetch(`${base}/api/v1/media/delete-intent`, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
      Authorization: `Bearer ${token}`,
    },
    body: JSON.stringify({ file_path: filePath }),
  })
  if (response.status !== 202) {
    throw new Error(`Request failed with status ${response.status}`)
  }
  return response.json()
}

export function createNotificationWaiter() {
  // pending: requestId -> { resolve, reject, eventTypes: Set, timer }
  const pending = new Map()

  return {
    waitFor(keys, eventTypes, timeoutMs) {
      const keyArray = Array.isArray(keys) ? keys : [keys]
      return new Promise((resolve, reject) => {
        const timer = setTimeout(() => {
          keyArray.forEach(k => pending.delete(k))
          reject(new Error(`Notification timeout for request ${keyArray[0]}`))
        }, timeoutMs)
        const entry = { resolve, reject, eventTypes: new Set(eventTypes), timer, keys: keyArray }
        keyArray.forEach(k => pending.set(k, entry))
      })
    },

    handleNotification(notification) {
      const data = JSON.parse(notification.payload || '{}')
      // Match by request_id first, then fall back to file_path.
      // Some notifications (upload_completed, file_deleted) lack request_id
      // but always carry file_path.
      for (const key of [data.request_id, data.file_path].filter(Boolean)) {
        const waiter = pending.get(key)
        if (!waiter) continue
        if (!waiter.eventTypes.has(notification.event_type)) continue
        clearTimeout(waiter.timer)
        waiter.keys.forEach(k => pending.delete(k))
        waiter.resolve(notification)
        return
      }
    },

    cleanup() {
      for (const [, waiter] of pending) {
        clearTimeout(waiter.timer)
        waiter.reject(new Error('Notification waiter cleaned up'))
      }
      pending.clear()
    },
  }
}
