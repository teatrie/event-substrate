import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import {
  requestUploadUrl,
  uploadFileToStorage,
  fetchUserFiles,
  formatFileSize,
  isAllowedMediaType,
  getMediaCategory,
  InsufficientCreditsError,
  requestDownloadUrl,
  deleteFile,
  requestUploadIntent,
  requestDownloadIntent,
  requestDeleteIntent,
  createNotificationWaiter,
} from './media.js'

// ---------------------------------------------------------------------------
// requestUploadUrl
// ---------------------------------------------------------------------------
describe('requestUploadUrl', () => {
  const apiGatewayUrl = 'http://localhost:8080'
  const token = 'test-jwt-token'
  const fileMetadata = {
    file_name: 'photo.jpg',
    media_type: 'image/jpeg',
    file_size: 204800,
  }

  beforeEach(() => {
    globalThis.fetch = vi.fn()
  })

  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('sends POST to correct endpoint with auth header and JSON body', async () => {
    globalThis.fetch.mockResolvedValueOnce({
      ok: true,
      status: 200,
      json: async () => ({
        upload_url: 'https://storage.example.com/upload/abc',
        file_path: 'uploads/photo.jpg',
        expires_in: 3600,
      }),
    })

    await requestUploadUrl(apiGatewayUrl, token, fileMetadata)

    expect(globalThis.fetch).toHaveBeenCalledWith(
      'http://localhost:8080/api/v1/media/upload-url',
      expect.objectContaining({
        method: 'POST',
        headers: expect.objectContaining({
          'Content-Type': 'application/json',
          Authorization: 'Bearer test-jwt-token',
        }),
        body: JSON.stringify(fileMetadata),
      }),
    )
  })

  it('returns parsed JSON response on success', async () => {
    const responsePayload = {
      upload_url: 'https://storage.example.com/upload/abc',
      file_path: 'uploads/photo.jpg',
      expires_in: 3600,
    }
    globalThis.fetch.mockResolvedValueOnce({
      ok: true,
      status: 200,
      json: async () => responsePayload,
    })

    const result = await requestUploadUrl(apiGatewayUrl, token, fileMetadata)

    expect(result).toEqual(responsePayload)
    expect(result).toHaveProperty('upload_url')
    expect(result).toHaveProperty('file_path')
    expect(result).toHaveProperty('expires_in')
  })

  it('throws InsufficientCreditsError on 402 status', async () => {
    globalThis.fetch.mockResolvedValueOnce({
      ok: false,
      status: 402,
      text: async () => 'Payment Required',
    })

    await expect(
      requestUploadUrl(apiGatewayUrl, token, fileMetadata),
    ).rejects.toThrow(InsufficientCreditsError)
  })

  it('throws generic error on 500 status', async () => {
    globalThis.fetch.mockResolvedValueOnce({
      ok: false,
      status: 500,
      text: async () => 'Internal Server Error',
    })

    await expect(
      requestUploadUrl(apiGatewayUrl, token, fileMetadata),
    ).rejects.toThrow()
  })

  it('throws generic error on 403 status', async () => {
    globalThis.fetch.mockResolvedValueOnce({
      ok: false,
      status: 403,
      text: async () => 'Forbidden',
    })

    await expect(
      requestUploadUrl(apiGatewayUrl, token, fileMetadata),
    ).rejects.toThrow()
  })

  it('throws generic error on 404 status', async () => {
    globalThis.fetch.mockResolvedValueOnce({
      ok: false,
      status: 404,
      text: async () => 'Not Found',
    })

    await expect(
      requestUploadUrl(apiGatewayUrl, token, fileMetadata),
    ).rejects.toThrow()
  })

  it('throws when network request fails entirely', async () => {
    globalThis.fetch.mockRejectedValueOnce(new TypeError('Failed to fetch'))

    await expect(
      requestUploadUrl(apiGatewayUrl, token, fileMetadata),
    ).rejects.toThrow()
  })

  it('uses the provided apiGatewayUrl without trailing slash duplication', async () => {
    globalThis.fetch.mockResolvedValueOnce({
      ok: true,
      status: 200,
      json: async () => ({ upload_url: '', file_path: '', expires_in: 0 }),
    })

    await requestUploadUrl('http://gateway:9090', token, fileMetadata)

    expect(globalThis.fetch).toHaveBeenCalledWith(
      'http://gateway:9090/api/v1/media/upload-url',
      expect.anything(),
    )
  })

  it('handles apiGatewayUrl with trailing slash', async () => {
    globalThis.fetch.mockResolvedValueOnce({
      ok: true,
      status: 200,
      json: async () => ({ upload_url: '', file_path: '', expires_in: 0 }),
    })

    await requestUploadUrl('http://gateway:9090/', token, fileMetadata)

    const calledUrl = globalThis.fetch.mock.calls[0][0]
    // Should not produce double-slash like http://gateway:9090//api/v1/...
    expect(calledUrl).not.toContain('//api')
  })
})

// ---------------------------------------------------------------------------
// uploadFileToStorage
// ---------------------------------------------------------------------------
describe('uploadFileToStorage', () => {
  const uploadUrl = 'https://storage.example.com/upload/abc123'
  // Blob constructor sets .type as a read-only getter -- no need to reassign
  const file = new Blob(['file-contents'], { type: 'image/png' })

  beforeEach(() => {
    globalThis.fetch = vi.fn()
  })

  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('sends PUT request to the provided upload URL', async () => {
    globalThis.fetch.mockResolvedValueOnce({ ok: true, status: 200 })

    await uploadFileToStorage(uploadUrl, file)

    expect(globalThis.fetch).toHaveBeenCalledWith(
      uploadUrl,
      expect.objectContaining({ method: 'PUT' }),
    )
  })

  it('sets Content-Type header from file.type', async () => {
    globalThis.fetch.mockResolvedValueOnce({ ok: true, status: 200 })

    await uploadFileToStorage(uploadUrl, file)

    const callArgs = globalThis.fetch.mock.calls[0][1]
    expect(callArgs.headers['Content-Type']).toBe('image/png')
  })

  it('sends the raw file as the request body', async () => {
    globalThis.fetch.mockResolvedValueOnce({ ok: true, status: 200 })

    await uploadFileToStorage(uploadUrl, file)

    const callArgs = globalThis.fetch.mock.calls[0][1]
    expect(callArgs.body).toBe(file)
  })

  it('returns { success: true } on 200', async () => {
    globalThis.fetch.mockResolvedValueOnce({ ok: true, status: 200 })

    const result = await uploadFileToStorage(uploadUrl, file)

    expect(result).toEqual({ success: true })
  })

  it('throws on 403 status', async () => {
    globalThis.fetch.mockResolvedValueOnce({
      ok: false,
      status: 403,
      text: async () => 'Forbidden',
    })

    await expect(uploadFileToStorage(uploadUrl, file)).rejects.toThrow()
  })

  it('throws on 500 status', async () => {
    globalThis.fetch.mockResolvedValueOnce({
      ok: false,
      status: 500,
      text: async () => 'Server Error',
    })

    await expect(uploadFileToStorage(uploadUrl, file)).rejects.toThrow()
  })

  it('throws on network failure', async () => {
    globalThis.fetch.mockRejectedValueOnce(new TypeError('Network error'))

    await expect(uploadFileToStorage(uploadUrl, file)).rejects.toThrow()
  })

  it('handles video file type correctly', async () => {
    const videoFile = new Blob(['video-data'], { type: 'video/mp4' })
    globalThis.fetch.mockResolvedValueOnce({ ok: true, status: 200 })

    await uploadFileToStorage(uploadUrl, videoFile)

    const callArgs = globalThis.fetch.mock.calls[0][1]
    expect(callArgs.headers['Content-Type']).toBe('video/mp4')
  })
})

// ---------------------------------------------------------------------------
// fetchUserFiles
// ---------------------------------------------------------------------------
describe('fetchUserFiles', () => {
  function createMockSupabaseClient(data, error = null) {
    const orderMock = vi.fn(() => Promise.resolve({ data, error }))
    const selectMock = vi.fn(() => ({ order: orderMock }))
    const fromResult = { select: selectMock }
    return {
      from: vi.fn(() => fromResult),
      _selectMock: selectMock,
      _orderMock: orderMock,
    }
  }

  it('queries the media_files table', async () => {
    const mockClient = createMockSupabaseClient([])

    await fetchUserFiles(mockClient)

    expect(mockClient.from).toHaveBeenCalledWith('media_files')
  })

  it('selects all required columns', async () => {
    const mockClient = createMockSupabaseClient([])

    await fetchUserFiles(mockClient)

    expect(mockClient._selectMock).toHaveBeenCalled()
  })

  it('orders by created_at descending', async () => {
    const mockClient = createMockSupabaseClient([])

    await fetchUserFiles(mockClient)

    expect(mockClient._orderMock).toHaveBeenCalledWith('created_at', { ascending: false })
  })

  it('returns array of file objects on success', async () => {
    const files = [
      {
        id: '1',
        file_name: 'photo.jpg',
        file_path: 'uploads/photo.jpg',
        file_size: 204800,
        media_type: 'image/jpeg',
        upload_time: '2026-01-01T00:00:00Z',
      },
      {
        id: '2',
        file_name: 'video.mp4',
        file_path: 'uploads/video.mp4',
        file_size: 10485760,
        media_type: 'video/mp4',
        upload_time: '2026-01-02T00:00:00Z',
      },
    ]
    const mockClient = createMockSupabaseClient(files)

    const result = await fetchUserFiles(mockClient)

    expect(result).toEqual(files)
    expect(result).toHaveLength(2)
    expect(result[0]).toHaveProperty('id')
    expect(result[0]).toHaveProperty('file_name')
    expect(result[0]).toHaveProperty('file_path')
    expect(result[0]).toHaveProperty('file_size')
    expect(result[0]).toHaveProperty('media_type')
    expect(result[0]).toHaveProperty('upload_time')
  })

  it('returns empty array when no files exist', async () => {
    const mockClient = createMockSupabaseClient([])

    const result = await fetchUserFiles(mockClient)

    expect(result).toEqual([])
  })

  it('throws when supabase returns an error', async () => {
    const mockClient = createMockSupabaseClient(null, { message: 'Table not found' })

    await expect(fetchUserFiles(mockClient)).rejects.toThrow()
  })

  it('throws when supabase client is null', async () => {
    await expect(fetchUserFiles(null)).rejects.toThrow()
  })

  it('throws when supabase client is undefined', async () => {
    await expect(fetchUserFiles(undefined)).rejects.toThrow()
  })
})

// ---------------------------------------------------------------------------
// formatFileSize
// ---------------------------------------------------------------------------
describe('formatFileSize', () => {
  it('returns "0 B" for 0 bytes', () => {
    expect(formatFileSize(0)).toBe('0 B')
  })

  it('formats bytes under 1 KB', () => {
    expect(formatFileSize(512)).toBe('512 B')
  })

  it('formats exactly 1 KB', () => {
    expect(formatFileSize(1024)).toBe('1 KB')
  })

  it('formats kilobytes with decimal', () => {
    const result = formatFileSize(256 * 1024)
    expect(result).toBe('256 KB')
  })

  it('formats fractional kilobytes', () => {
    const result = formatFileSize(1536) // 1.5 KB
    expect(result).toBe('1.5 KB')
  })

  it('formats exactly 1 MB', () => {
    expect(formatFileSize(1024 * 1024)).toBe('1 MB')
  })

  it('formats megabytes with decimal', () => {
    const result = formatFileSize(1.5 * 1024 * 1024)
    expect(result).toBe('1.5 MB')
  })

  it('formats exactly 1 GB', () => {
    expect(formatFileSize(1024 * 1024 * 1024)).toBe('1 GB')
  })

  it('formats gigabytes with decimal', () => {
    const result = formatFileSize(2.1 * 1024 * 1024 * 1024)
    expect(result).toBe('2.1 GB')
  })

  it('formats large gigabyte values', () => {
    const result = formatFileSize(500 * 1024 * 1024 * 1024)
    expect(result).toBe('500 GB')
  })

  it('formats terabytes', () => {
    const result = formatFileSize(1024 * 1024 * 1024 * 1024)
    expect(result).toBe('1 TB')
  })

  it('handles 1 byte', () => {
    expect(formatFileSize(1)).toBe('1 B')
  })

  it('handles negative input gracefully', () => {
    // Implementation may throw or return "0 B" -- either is acceptable
    // but it should not return something nonsensical
    const result = formatFileSize(-1)
    expect(typeof result).toBe('string')
  })
})

// ---------------------------------------------------------------------------
// isAllowedMediaType
// ---------------------------------------------------------------------------
describe('isAllowedMediaType', () => {
  const allowedTypes = [
    'image/jpeg',
    'image/png',
    'image/gif',
    'image/webp',
    'video/mp4',
    'video/webm',
    'audio/mpeg',
    'audio/wav',
    'audio/ogg',
  ]

  for (const mediaType of allowedTypes) {
    it(`returns true for allowed type: ${mediaType}`, () => {
      expect(isAllowedMediaType(mediaType)).toBe(true)
    })
  }

  const disallowedTypes = [
    'application/pdf',
    'text/plain',
    'text/html',
    'application/json',
    'application/zip',
    'image/bmp',
    'image/tiff',
    'video/avi',
    'audio/flac',
    'application/octet-stream',
  ]

  for (const mediaType of disallowedTypes) {
    it(`returns false for disallowed type: ${mediaType}`, () => {
      expect(isAllowedMediaType(mediaType)).toBe(false)
    })
  }

  it('returns false for empty string', () => {
    expect(isAllowedMediaType('')).toBe(false)
  })

  it('returns false for null', () => {
    expect(isAllowedMediaType(null)).toBe(false)
  })

  it('returns false for undefined', () => {
    expect(isAllowedMediaType(undefined)).toBe(false)
  })

  it('is case-sensitive (rejects uppercase)', () => {
    expect(isAllowedMediaType('IMAGE/JPEG')).toBe(false)
  })

  it('rejects types with leading/trailing whitespace', () => {
    expect(isAllowedMediaType(' image/jpeg ')).toBe(false)
  })
})

// ---------------------------------------------------------------------------
// getMediaCategory
// ---------------------------------------------------------------------------
describe('getMediaCategory', () => {
  it('returns "image" for image/jpeg', () => {
    expect(getMediaCategory('image/jpeg')).toBe('image')
  })

  it('returns "image" for image/png', () => {
    expect(getMediaCategory('image/png')).toBe('image')
  })

  it('returns "image" for image/gif', () => {
    expect(getMediaCategory('image/gif')).toBe('image')
  })

  it('returns "image" for image/webp', () => {
    expect(getMediaCategory('image/webp')).toBe('image')
  })

  it('returns "image" for image/bmp (any image/* type)', () => {
    expect(getMediaCategory('image/bmp')).toBe('image')
  })

  it('returns "video" for video/mp4', () => {
    expect(getMediaCategory('video/mp4')).toBe('video')
  })

  it('returns "video" for video/webm', () => {
    expect(getMediaCategory('video/webm')).toBe('video')
  })

  it('returns "video" for video/avi (any video/* type)', () => {
    expect(getMediaCategory('video/avi')).toBe('video')
  })

  it('returns "audio" for audio/mpeg', () => {
    expect(getMediaCategory('audio/mpeg')).toBe('audio')
  })

  it('returns "audio" for audio/wav', () => {
    expect(getMediaCategory('audio/wav')).toBe('audio')
  })

  it('returns "audio" for audio/ogg', () => {
    expect(getMediaCategory('audio/ogg')).toBe('audio')
  })

  it('returns "audio" for audio/flac (any audio/* type)', () => {
    expect(getMediaCategory('audio/flac')).toBe('audio')
  })

  it('returns "unknown" for application/pdf', () => {
    expect(getMediaCategory('application/pdf')).toBe('unknown')
  })

  it('returns "unknown" for text/plain', () => {
    expect(getMediaCategory('text/plain')).toBe('unknown')
  })

  it('returns "unknown" for empty string', () => {
    expect(getMediaCategory('')).toBe('unknown')
  })

  it('returns "unknown" for null', () => {
    expect(getMediaCategory(null)).toBe('unknown')
  })

  it('returns "unknown" for undefined', () => {
    expect(getMediaCategory(undefined)).toBe('unknown')
  })
})

// ---------------------------------------------------------------------------
// requestDownloadUrl
// ---------------------------------------------------------------------------
describe('requestDownloadUrl', () => {
  const apiGatewayUrl = 'http://localhost:8080'
  const token = 'test-jwt-token'
  const filePath = 'uploads/photo.jpg'

  beforeEach(() => {
    globalThis.fetch = vi.fn()
  })

  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('calls correct endpoint /api/v1/media/download-url with POST method', async () => {
    globalThis.fetch.mockResolvedValueOnce({
      ok: true,
      status: 200,
      json: async () => ({
        download_url: 'https://storage.example.com/download/abc',
        expires_in: 3600,
      }),
    })

    await requestDownloadUrl(apiGatewayUrl, token, filePath)

    expect(globalThis.fetch).toHaveBeenCalledWith(
      'http://localhost:8080/api/v1/media/download-url',
      expect.objectContaining({
        method: 'POST',
      }),
    )
  })

  it('sends Authorization Bearer header with token', async () => {
    globalThis.fetch.mockResolvedValueOnce({
      ok: true,
      status: 200,
      json: async () => ({
        download_url: 'https://storage.example.com/download/abc',
        expires_in: 3600,
      }),
    })

    await requestDownloadUrl(apiGatewayUrl, token, filePath)

    const callArgs = globalThis.fetch.mock.calls[0][1]
    expect(callArgs.headers).toEqual(
      expect.objectContaining({
        Authorization: 'Bearer test-jwt-token',
      }),
    )
  })

  it('sends file_path in request body', async () => {
    globalThis.fetch.mockResolvedValueOnce({
      ok: true,
      status: 200,
      json: async () => ({
        download_url: 'https://storage.example.com/download/abc',
        expires_in: 3600,
      }),
    })

    await requestDownloadUrl(apiGatewayUrl, token, filePath)

    const callArgs = globalThis.fetch.mock.calls[0][1]
    expect(JSON.parse(callArgs.body)).toEqual({ file_path: 'uploads/photo.jpg' })
  })

  it('returns parsed JSON response with download_url and expires_in', async () => {
    const responsePayload = {
      download_url: 'https://storage.example.com/download/abc',
      expires_in: 3600,
    }
    globalThis.fetch.mockResolvedValueOnce({
      ok: true,
      status: 200,
      json: async () => responsePayload,
    })

    const result = await requestDownloadUrl(apiGatewayUrl, token, filePath)

    expect(result).toEqual(responsePayload)
    expect(result).toHaveProperty('download_url')
    expect(result).toHaveProperty('expires_in')
  })

  it('throws on 404 status', async () => {
    globalThis.fetch.mockResolvedValueOnce({
      ok: false,
      status: 404,
      text: async () => 'Not Found',
    })

    await expect(
      requestDownloadUrl(apiGatewayUrl, token, filePath),
    ).rejects.toThrow()
  })

  it('throws on 500 status', async () => {
    globalThis.fetch.mockResolvedValueOnce({
      ok: false,
      status: 500,
      text: async () => 'Internal Server Error',
    })

    await expect(
      requestDownloadUrl(apiGatewayUrl, token, filePath),
    ).rejects.toThrow()
  })

  it('handles network failure', async () => {
    globalThis.fetch.mockRejectedValueOnce(new TypeError('Failed to fetch'))

    await expect(
      requestDownloadUrl(apiGatewayUrl, token, filePath),
    ).rejects.toThrow()
  })

  it('constructs URL without double slashes when apiGatewayUrl has trailing slash', async () => {
    globalThis.fetch.mockResolvedValueOnce({
      ok: true,
      status: 200,
      json: async () => ({ download_url: '', expires_in: 0 }),
    })

    await requestDownloadUrl('http://gateway:9090/', token, filePath)

    const calledUrl = globalThis.fetch.mock.calls[0][0]
    expect(calledUrl).not.toContain('//api')
  })
})

// ---------------------------------------------------------------------------
// deleteFile
// ---------------------------------------------------------------------------
describe('deleteFile', () => {
  const apiGatewayUrl = 'http://localhost:8080'
  const token = 'test-jwt-token'
  const filePath = 'uploads/photo.jpg'

  beforeEach(() => {
    globalThis.fetch = vi.fn()
  })

  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('calls correct endpoint /api/v1/media/delete with POST method', async () => {
    globalThis.fetch.mockResolvedValueOnce({
      ok: true,
      status: 200,
      json: async () => ({ success: true }),
    })

    await deleteFile(apiGatewayUrl, token, filePath)

    expect(globalThis.fetch).toHaveBeenCalledWith(
      'http://localhost:8080/api/v1/media/delete',
      expect.objectContaining({
        method: 'POST',
      }),
    )
  })

  it('sends Authorization Bearer header with token', async () => {
    globalThis.fetch.mockResolvedValueOnce({
      ok: true,
      status: 200,
      json: async () => ({ success: true }),
    })

    await deleteFile(apiGatewayUrl, token, filePath)

    const callArgs = globalThis.fetch.mock.calls[0][1]
    expect(callArgs.headers).toEqual(
      expect.objectContaining({
        Authorization: 'Bearer test-jwt-token',
      }),
    )
  })

  it('sends file_path in request body', async () => {
    globalThis.fetch.mockResolvedValueOnce({
      ok: true,
      status: 200,
      json: async () => ({ success: true }),
    })

    await deleteFile(apiGatewayUrl, token, filePath)

    const callArgs = globalThis.fetch.mock.calls[0][1]
    expect(JSON.parse(callArgs.body)).toEqual({ file_path: 'uploads/photo.jpg' })
  })

  it('returns parsed JSON response with success: true', async () => {
    const responsePayload = { success: true }
    globalThis.fetch.mockResolvedValueOnce({
      ok: true,
      status: 200,
      json: async () => responsePayload,
    })

    const result = await deleteFile(apiGatewayUrl, token, filePath)

    expect(result).toEqual(responsePayload)
    expect(result).toHaveProperty('success', true)
  })

  it('throws on 404 status (file not found)', async () => {
    globalThis.fetch.mockResolvedValueOnce({
      ok: false,
      status: 404,
      text: async () => 'Not Found',
    })

    await expect(
      deleteFile(apiGatewayUrl, token, filePath),
    ).rejects.toThrow()
  })

  it('throws on 500 status', async () => {
    globalThis.fetch.mockResolvedValueOnce({
      ok: false,
      status: 500,
      text: async () => 'Internal Server Error',
    })

    await expect(
      deleteFile(apiGatewayUrl, token, filePath),
    ).rejects.toThrow()
  })

  it('handles network failure', async () => {
    globalThis.fetch.mockRejectedValueOnce(new TypeError('Failed to fetch'))

    await expect(
      deleteFile(apiGatewayUrl, token, filePath),
    ).rejects.toThrow()
  })

  it('constructs URL without double slashes when apiGatewayUrl has trailing slash', async () => {
    globalThis.fetch.mockResolvedValueOnce({
      ok: true,
      status: 200,
      json: async () => ({ success: true }),
    })

    await deleteFile('http://gateway:9090/', token, filePath)

    const calledUrl = globalThis.fetch.mock.calls[0][0]
    expect(calledUrl).not.toContain('//api')
  })
})

// ---------------------------------------------------------------------------
// requestUploadIntent
// ---------------------------------------------------------------------------
describe('requestUploadIntent', () => {
  const apiGatewayUrl = 'http://localhost:8080'
  const token = 'test-jwt-token'
  const fileMetadata = {
    file_name: 'photo.jpg',
    media_type: 'image/jpeg',
    file_size: 204800,
  }

  beforeEach(() => {
    globalThis.fetch = vi.fn()
  })

  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('sends POST to /api/v1/media/upload-intent with auth header and JSON body', async () => {
    globalThis.fetch.mockResolvedValueOnce({
      ok: true,
      status: 202,
      json: async () => ({ request_id: 'req-abc-123' }),
    })

    await requestUploadIntent(apiGatewayUrl, token, fileMetadata)

    expect(globalThis.fetch).toHaveBeenCalledWith(
      'http://localhost:8080/api/v1/media/upload-intent',
      expect.objectContaining({
        method: 'POST',
        headers: expect.objectContaining({
          'Content-Type': 'application/json',
          Authorization: 'Bearer test-jwt-token',
        }),
        body: JSON.stringify(fileMetadata),
      }),
    )
  })

  it('returns parsed JSON with request_id on 202', async () => {
    globalThis.fetch.mockResolvedValueOnce({
      ok: true,
      status: 202,
      json: async () => ({ request_id: 'req-abc-123' }),
    })

    const result = await requestUploadIntent(apiGatewayUrl, token, fileMetadata)

    expect(result).toEqual({ request_id: 'req-abc-123' })
    expect(result).toHaveProperty('request_id')
  })

  it('throws InsufficientCreditsError on 402', async () => {
    globalThis.fetch.mockResolvedValueOnce({
      ok: false,
      status: 402,
      text: async () => 'Payment Required',
    })

    await expect(
      requestUploadIntent(apiGatewayUrl, token, fileMetadata),
    ).rejects.toThrow(InsufficientCreditsError)
  })

  it('throws on 500 status', async () => {
    globalThis.fetch.mockResolvedValueOnce({
      ok: false,
      status: 500,
      text: async () => 'Internal Server Error',
    })

    await expect(
      requestUploadIntent(apiGatewayUrl, token, fileMetadata),
    ).rejects.toThrow()
  })

  it('throws on 200 status (intent endpoint must return 202)', async () => {
    globalThis.fetch.mockResolvedValueOnce({
      ok: true,
      status: 200,
      json: async () => ({ upload_url: 'https://storage.example.com' }),
    })

    await expect(
      requestUploadIntent(apiGatewayUrl, token, fileMetadata),
    ).rejects.toThrow()
  })

  it('handles apiGatewayUrl with trailing slash', async () => {
    globalThis.fetch.mockResolvedValueOnce({
      ok: true,
      status: 202,
      json: async () => ({ request_id: 'req-xyz' }),
    })

    await requestUploadIntent('http://gateway:9090/', token, fileMetadata)

    const calledUrl = globalThis.fetch.mock.calls[0][0]
    expect(calledUrl).not.toContain('//api')
    expect(calledUrl).toBe('http://gateway:9090/api/v1/media/upload-intent')
  })
})

// ---------------------------------------------------------------------------
// requestDownloadIntent
// ---------------------------------------------------------------------------
describe('requestDownloadIntent', () => {
  const apiGatewayUrl = 'http://localhost:8080'
  const token = 'test-jwt-token'
  const filePath = 'uploads/photo.jpg'

  beforeEach(() => {
    globalThis.fetch = vi.fn()
  })

  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('sends POST to /api/v1/media/download-intent with auth header', async () => {
    globalThis.fetch.mockResolvedValueOnce({
      ok: true,
      status: 202,
      json: async () => ({ request_id: 'req-dl-123' }),
    })

    await requestDownloadIntent(apiGatewayUrl, token, filePath)

    expect(globalThis.fetch).toHaveBeenCalledWith(
      'http://localhost:8080/api/v1/media/download-intent',
      expect.objectContaining({
        method: 'POST',
        headers: expect.objectContaining({
          'Content-Type': 'application/json',
          Authorization: 'Bearer test-jwt-token',
        }),
        body: JSON.stringify({ file_path: filePath }),
      }),
    )
  })

  it('returns parsed JSON with request_id on 202', async () => {
    globalThis.fetch.mockResolvedValueOnce({
      ok: true,
      status: 202,
      json: async () => ({ request_id: 'req-dl-123' }),
    })

    const result = await requestDownloadIntent(apiGatewayUrl, token, filePath)

    expect(result).toHaveProperty('request_id', 'req-dl-123')
  })

  it('throws on non-202 status', async () => {
    globalThis.fetch.mockResolvedValueOnce({
      ok: false,
      status: 404,
      text: async () => 'Not Found',
    })

    await expect(
      requestDownloadIntent(apiGatewayUrl, token, filePath),
    ).rejects.toThrow()
  })

  it('throws on 200 status (intent endpoint must return 202)', async () => {
    globalThis.fetch.mockResolvedValueOnce({
      ok: true,
      status: 200,
      json: async () => ({ download_url: 'https://storage.example.com' }),
    })

    await expect(
      requestDownloadIntent(apiGatewayUrl, token, filePath),
    ).rejects.toThrow()
  })
})

// ---------------------------------------------------------------------------
// requestDeleteIntent
// ---------------------------------------------------------------------------
describe('requestDeleteIntent', () => {
  const apiGatewayUrl = 'http://localhost:8080'
  const token = 'test-jwt-token'
  const filePath = 'uploads/photo.jpg'

  beforeEach(() => {
    globalThis.fetch = vi.fn()
  })

  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('sends POST to /api/v1/media/delete-intent with auth header', async () => {
    globalThis.fetch.mockResolvedValueOnce({
      ok: true,
      status: 202,
      json: async () => ({ request_id: 'req-del-123' }),
    })

    await requestDeleteIntent(apiGatewayUrl, token, filePath)

    expect(globalThis.fetch).toHaveBeenCalledWith(
      'http://localhost:8080/api/v1/media/delete-intent',
      expect.objectContaining({
        method: 'POST',
        headers: expect.objectContaining({
          'Content-Type': 'application/json',
          Authorization: 'Bearer test-jwt-token',
        }),
        body: JSON.stringify({ file_path: filePath }),
      }),
    )
  })

  it('returns parsed JSON with request_id on 202', async () => {
    globalThis.fetch.mockResolvedValueOnce({
      ok: true,
      status: 202,
      json: async () => ({ request_id: 'req-del-123' }),
    })

    const result = await requestDeleteIntent(apiGatewayUrl, token, filePath)

    expect(result).toHaveProperty('request_id', 'req-del-123')
  })

  it('throws on non-202 status', async () => {
    globalThis.fetch.mockResolvedValueOnce({
      ok: false,
      status: 500,
      text: async () => 'Internal Server Error',
    })

    await expect(
      requestDeleteIntent(apiGatewayUrl, token, filePath),
    ).rejects.toThrow()
  })

  it('throws on 200 status (intent endpoint must return 202)', async () => {
    globalThis.fetch.mockResolvedValueOnce({
      ok: true,
      status: 200,
      json: async () => ({ success: true }),
    })

    await expect(
      requestDeleteIntent(apiGatewayUrl, token, filePath),
    ).rejects.toThrow()
  })
})

// ---------------------------------------------------------------------------
// createNotificationWaiter
// ---------------------------------------------------------------------------
describe('createNotificationWaiter', () => {
  afterEach(() => {
    vi.useRealTimers()
  })

  it('resolves waitFor when handleNotification delivers a matching notification', async () => {
    const waiter = createNotificationWaiter()
    const notification = {
      request_id: 'req-123',
      event_type: 'media.upload_ready',
      payload: JSON.stringify({ upload_url: 'https://storage.example.com' }),
    }

    const promise = waiter.waitFor('req-123', ['media.upload_ready', 'media.upload_rejected'], 5000)
    waiter.handleNotification(notification)

    const result = await promise
    expect(result).toEqual(notification)
  })

  it('resolves with rejection notification when event_type is in expected set', async () => {
    const waiter = createNotificationWaiter()
    const rejectionNotification = {
      request_id: 'req-456',
      event_type: 'media.upload_rejected',
      payload: JSON.stringify({ reason: 'Insufficient credits' }),
    }

    const promise = waiter.waitFor('req-456', ['media.upload_ready', 'media.upload_rejected'], 5000)
    waiter.handleNotification(rejectionNotification)

    const result = await promise
    expect(result).toEqual(rejectionNotification)
  })

  it('does not resolve when event_type does not match expected set', async () => {
    vi.useFakeTimers()
    const waiter = createNotificationWaiter()
    const wrongTypeNotification = {
      request_id: 'req-789',
      event_type: 'media.download_ready',
      payload: JSON.stringify({}),
    }

    const promise = waiter.waitFor('req-789', ['media.upload_ready', 'media.upload_rejected'], 1000)
    waiter.handleNotification(wrongTypeNotification)
    vi.advanceTimersByTime(1001)

    await expect(promise).rejects.toThrow()
  })

  it('rejects on timeout when no matching notification arrives', async () => {
    vi.useFakeTimers()
    const waiter = createNotificationWaiter()

    const promise = waiter.waitFor('req-timeout', ['media.upload_ready'], 1000)
    vi.advanceTimersByTime(1001)

    await expect(promise).rejects.toThrow()
  })

  it('handles multiple concurrent waiters independently', async () => {
    const waiter = createNotificationWaiter()
    const n1 = { request_id: 'req-a', event_type: 'media.upload_ready', payload: '{}' }
    const n2 = { request_id: 'req-b', event_type: 'media.download_ready', payload: '{}' }

    const p1 = waiter.waitFor('req-a', ['media.upload_ready'], 5000)
    const p2 = waiter.waitFor('req-b', ['media.download_ready'], 5000)

    waiter.handleNotification(n1)
    waiter.handleNotification(n2)

    const [r1, r2] = await Promise.all([p1, p2])
    expect(r1).toEqual(n1)
    expect(r2).toEqual(n2)
  })

  it('ignores notifications with a non-matching request_id', async () => {
    vi.useFakeTimers()
    const waiter = createNotificationWaiter()
    const wrongIdNotification = {
      request_id: 'req-different',
      event_type: 'media.upload_ready',
      payload: '{}',
    }

    const promise = waiter.waitFor('req-correct', ['media.upload_ready'], 1000)
    waiter.handleNotification(wrongIdNotification)
    vi.advanceTimersByTime(1001)

    await expect(promise).rejects.toThrow()
  })

  it('cleanup rejects all pending waiters', async () => {
    const waiter = createNotificationWaiter()

    const promise = waiter.waitFor('req-cleanup', ['media.upload_ready'], 5000)
    waiter.cleanup()

    await expect(promise).rejects.toThrow()
  })

  it('handleNotification is a no-op when no waiters are pending', () => {
    const waiter = createNotificationWaiter()
    expect(() => {
      waiter.handleNotification({ request_id: 'req-none', event_type: 'media.upload_ready', payload: '{}' })
    }).not.toThrow()
  })
})
