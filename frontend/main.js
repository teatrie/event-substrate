import './style.css'
import { createClient } from '@supabase/supabase-js'
import { requestUploadUrl, uploadFileToStorage, fetchUserFiles, formatFileSize, isAllowedMediaType, getMediaCategory, InsufficientCreditsError, requestDownloadUrl, deleteFile } from './media.js'

const supabaseUrl = import.meta.env.VITE_SUPABASE_URL || 'http://127.0.0.1:54321'
const supabaseAnonKey = import.meta.env.VITE_SUPABASE_ANON_KEY
const apiGatewayUrl = import.meta.env.VITE_API_GATEWAY_URL || 'http://localhost:8080'

if (!supabaseAnonKey) {
    console.error("VITE_SUPABASE_ANON_KEY is missing. Please create a .env file locally with the key.")
}

const supabase = createClient(supabaseUrl, supabaseAnonKey)

// DOM Elements
const authView = document.getElementById('auth-view')
const homeView = document.getElementById('home-view')
const authForm = document.getElementById('auth-form')
const emailInput = document.getElementById('email')
const passwordInput = document.getElementById('password')
const submitBtn = document.getElementById('submit-btn')
const toggleModeBtn = document.getElementById('toggle-mode')
const togglePrompt = document.getElementById('toggle-prompt')
const formTitle = document.getElementById('form-title')
const formSubtitle = document.getElementById('form-subtitle')
const alertBox = document.getElementById('alert-box')
const welcomeMessage = document.getElementById('welcome-message')
const logoutBtn = document.getElementById('logout-btn')
const messageForm = document.getElementById('message-form')
const messageInput = document.getElementById('message-input')
const sendBtn = document.getElementById('send-message-btn')

// Media DOM Elements
const mediaSection = document.getElementById('media-section')
const creditCount = document.getElementById('credit-count')
const creditBadge = document.getElementById('credit-badge')
const uploadForm = document.getElementById('upload-form')
const fileInput = document.getElementById('file-input')
const uploadBtn = document.getElementById('upload-btn')
const uploadLabel = document.getElementById('upload-label')
const uploadDropzone = document.getElementById('upload-dropzone')
const uploadStatus = document.getElementById('upload-status')
const mediaList = document.getElementById('media-list')

let isSignupMode = false
let selectedFile = null

// Toggle between Login and Signup modes
toggleModeBtn.addEventListener('click', () => {
    isSignupMode = !isSignupMode
    if (isSignupMode) {
        formTitle.innerText = "Create an account"
        formSubtitle.innerText = "Sign up to get started"
        submitBtn.innerText = "Sign Up"
        togglePrompt.innerText = "Already have an account?"
        toggleModeBtn.innerText = "Log in"
    } else {
        formTitle.innerText = "Welcome back"
        formSubtitle.innerText = "Enter your details to log in to your account"
        submitBtn.innerText = "Log In"
        togglePrompt.innerText = "Don't have an account?"
        toggleModeBtn.innerText = "Sign up"
    }
    hideAlert()
})

// Display alerts
function showAlert(message, type = 'error') {
    alertBox.className = `alert ${type}`
    alertBox.innerText = message
}

function hideAlert() {
    alertBox.className = 'alert'
    alertBox.innerText = ''
}

// Handle Form Submission
authForm.addEventListener('submit', async (e) => {
    e.preventDefault()
    submitBtn.disabled = true
    hideAlert()

    const email = emailInput.value
    const password = passwordInput.value

    try {
        if (isSignupMode) {
            const { data, error } = await supabase.auth.signUp({ email, password })
            if (error) throw error
            showAlert('Signup successful! Check your email or try logging in.', 'success')
            // Switch back to login mode after 2 seconds
            setTimeout(() => {
                if (isSignupMode) toggleModeBtn.click()
                passwordInput.value = ''
            }, 2000)
        } else {
            const { data, error } = await supabase.auth.signInWithPassword({ email, password })
            if (error) throw error
            checkSession()
        }
    } catch (err) {
        showAlert(err.message || 'An error occurred during authentication.')
    } finally {
        submitBtn.disabled = false
    }
})

// Handle Logout
logoutBtn.addEventListener('click', async () => {
    await supabase.auth.signOut()
    checkSession()
})

// Handle Message Broadcasting
if (messageForm) {
    messageForm.addEventListener('submit', async (e) => {
        e.preventDefault()
        const text = messageInput.value.trim()
        if (!text) return

        sendBtn.disabled = true
        sendBtn.innerText = "Sending..."

        try {
            const { data: { session } } = await supabase.auth.getSession()
            if (!session) throw new Error("Not authenticated")

            const payload = {
                user_id: session.user.id,
                email: session.user.email,
                message: text,
                timestamp: new Date().toISOString(),
                visibility: 'broadcast',
                recipient_id: null
            }

            const response = await fetch(`${apiGatewayUrl}/api/v1/events/message`, {
                method: 'POST',
                headers: {
                    'Content-Type': 'application/json',
                    'Authorization': `Bearer ${session.access_token}`
                },
                body: JSON.stringify(payload)
            })

            if (!response.ok) {
                const errText = await response.text()
                throw new Error(`API Error: ${errText}`)
            }

            messageInput.value = ''
        } catch (err) {
            console.error("Failed to send message event:", err)
            showAlert(err.message, 'error')
        } finally {
            sendBtn.disabled = false
            sendBtn.innerText = "Send"
        }
    })
}

// ─── Media Upload ───

// Fetch and display credit balance
async function refreshCredits() {
    try {
        const { data: { session } } = await supabase.auth.getSession()
        if (!session) return 0

        const { data, error } = await supabase
            .from('user_credit_balances')
            .select('balance')
            .eq('user_id', session.user.id)
            .maybeSingle()

        const balance = data?.balance ?? 0
        creditCount.textContent = balance
        creditBadge.classList.toggle('empty', balance <= 0)

        // Disable upload if no credits
        if (balance <= 0) {
            uploadBtn.disabled = true
            uploadStatus.textContent = 'No credits remaining — earn more to upload.'
            uploadStatus.className = 'upload-status error'
        } else {
            uploadStatus.textContent = ''
            uploadStatus.className = 'upload-status'
        }

        return balance
    } catch (err) {
        console.error('Failed to fetch credits:', err)
        creditCount.textContent = '?'
        return 0
    }
}

// File input change
fileInput.addEventListener('change', () => {
    const file = fileInput.files[0]
    if (!file) {
        selectedFile = null
        uploadLabel.textContent = 'Choose a file or drag it here'
        uploadDropzone.classList.remove('has-file')
        uploadBtn.disabled = true
        return
    }
    if (!isAllowedMediaType(file.type)) {
        selectedFile = null
        uploadStatus.textContent = `Unsupported file type: ${file.type}`
        uploadStatus.className = 'upload-status error'
        fileInput.value = ''
        uploadBtn.disabled = true
        return
    }
    selectedFile = file
    uploadLabel.textContent = `${file.name} (${formatFileSize(file.size)})`
    uploadDropzone.classList.add('has-file')
    uploadBtn.disabled = false
    uploadStatus.textContent = ''
    uploadStatus.className = 'upload-status'
})

// Drag-and-drop
uploadDropzone.addEventListener('dragover', (e) => { e.preventDefault(); uploadDropzone.classList.add('dragover') })
uploadDropzone.addEventListener('dragleave', () => uploadDropzone.classList.remove('dragover'))
uploadDropzone.addEventListener('drop', (e) => {
    e.preventDefault()
    uploadDropzone.classList.remove('dragover')
    if (e.dataTransfer.files.length) {
        fileInput.files = e.dataTransfer.files
        fileInput.dispatchEvent(new Event('change'))
    }
})

// Upload form submit
uploadForm.addEventListener('submit', async (e) => {
    e.preventDefault()
    if (!selectedFile) return

    uploadBtn.disabled = true
    uploadBtn.textContent = 'Uploading…'
    uploadStatus.textContent = 'Requesting upload URL…'
    uploadStatus.className = 'upload-status'

    try {
        const { data: { session } } = await supabase.auth.getSession()
        if (!session) throw new Error('Not authenticated')

        const metadata = {
            file_name: selectedFile.name,
            media_type: selectedFile.type,
            file_size: selectedFile.size
        }

        const { upload_url } = await requestUploadUrl(apiGatewayUrl, session.access_token, metadata)

        uploadStatus.textContent = 'Uploading file…'
        await uploadFileToStorage(upload_url, selectedFile)

        uploadStatus.textContent = '✓ Upload complete'
        uploadStatus.className = 'upload-status success'

        // Reset form
        selectedFile = null
        fileInput.value = ''
        uploadLabel.textContent = 'Choose a file or drag it here'
        uploadDropzone.classList.remove('has-file')

        // Refresh credits and file list
        await Promise.all([refreshCredits(), refreshMediaBrowser()])

    } catch (err) {
        if (err instanceof InsufficientCreditsError) {
            uploadStatus.textContent = 'Insufficient credits to upload.'
        } else {
            uploadStatus.textContent = `Upload failed: ${err.message}`
        }
        uploadStatus.className = 'upload-status error'
        console.error('Media upload failed:', err)
    } finally {
        uploadBtn.disabled = false
        uploadBtn.textContent = 'Upload'
    }
})

// ─── Delete Confirmation Modal ───

const deleteModal = document.getElementById('delete-modal')
const deleteModalFilename = document.getElementById('delete-modal-filename')
const deleteConfirmInput = document.getElementById('delete-confirm-input')
const deleteConfirmBtn = document.getElementById('delete-confirm-btn')
const deleteCancelBtn = document.getElementById('delete-cancel-btn')

let pendingDeleteResolve = null

deleteConfirmInput.addEventListener('input', () => {
    deleteConfirmBtn.disabled = deleteConfirmInput.value.trim().toLowerCase() !== 'delete'
})

deleteCancelBtn.addEventListener('click', () => {
    deleteModal.style.display = 'none'
    deleteConfirmInput.value = ''
    deleteConfirmBtn.disabled = true
    if (pendingDeleteResolve) pendingDeleteResolve(false)
    pendingDeleteResolve = null
})

deleteConfirmBtn.addEventListener('click', () => {
    deleteModal.style.display = 'none'
    deleteConfirmInput.value = ''
    deleteConfirmBtn.disabled = true
    if (pendingDeleteResolve) pendingDeleteResolve(true)
    pendingDeleteResolve = null
})

deleteModal.addEventListener('click', (e) => {
    if (e.target === deleteModal) deleteCancelBtn.click()
})

function confirmDelete(fileName) {
    return new Promise((resolve) => {
        pendingDeleteResolve = resolve
        deleteModalFilename.textContent = fileName
        deleteConfirmInput.value = ''
        deleteConfirmBtn.disabled = true
        deleteModal.style.display = 'flex'
        deleteConfirmInput.focus()
    })
}

// ─── Media Browser ───

const CATEGORY_ICONS = { image: '🖼️', video: '🎬', audio: '🎵', unknown: '📄' }

async function refreshMediaBrowser() {
    try {
        const files = await fetchUserFiles(supabase)
        mediaList.innerHTML = ''

        if (!files.length) {
            mediaList.innerHTML = '<p class="media-empty">No files uploaded yet.</p>'
            return
        }

        files.forEach(f => {
            const cat = getMediaCategory(f.media_type)
            const card = document.createElement('div')
            card.className = 'media-file-card'
            card.innerHTML = `
                <span class="file-icon">${CATEGORY_ICONS[cat] || CATEGORY_ICONS.unknown}</span>
                <div class="file-info">
                    <div class="file-name" title="${f.file_name}">${f.file_name}</div>
                    <div class="file-meta">${formatFileSize(f.file_size)} · ${cat} · ${new Date(f.upload_time).toLocaleString()}</div>
                </div>
                <div class="file-actions">
                    <button class="btn-icon btn-download" data-file-path="${f.file_path}" title="Download">⬇️</button>
                    <button class="btn-icon btn-delete" data-file-path="${f.file_path}" title="Delete">🗑️</button>
                </div>
            `

            card.querySelector('.btn-download').addEventListener('click', async (e) => {
                const btn = e.currentTarget
                btn.disabled = true
                try {
                    const { data: { session } } = await supabase.auth.getSession()
                    if (!session) throw new Error('Not authenticated')
                    const { download_url } = await requestDownloadUrl(apiGatewayUrl, session.access_token, f.file_path)
                    window.open(download_url, '_blank')
                } catch (err) {
                    console.error('Download failed:', err)
                    uploadStatus.textContent = `Download failed: ${err.message}`
                    uploadStatus.className = 'upload-status error'
                } finally {
                    btn.disabled = false
                }
            })

            card.querySelector('.btn-delete').addEventListener('click', async (e) => {
                const btn = e.currentTarget
                const confirmed = await confirmDelete(f.file_name)
                if (!confirmed) return

                btn.disabled = true
                try {
                    const { data: { session } } = await supabase.auth.getSession()
                    if (!session) throw new Error('Not authenticated')
                    await deleteFile(apiGatewayUrl, session.access_token, f.file_path)
                    await Promise.all([refreshCredits(), refreshMediaBrowser()])
                } catch (err) {
                    console.error('Delete failed:', err)
                    uploadStatus.textContent = `Delete failed: ${err.message}`
                    uploadStatus.className = 'upload-status error'
                } finally {
                    btn.disabled = false
                }
            })

            mediaList.appendChild(card)
        })
    } catch (err) {
        console.error('Failed to load media files:', err)
        mediaList.innerHTML = '<p class="media-empty">Failed to load files.</p>'
    }
}

// Pre-fetch Recent Events history from unified notifications table
async function fetchHistory(userId) {
    const list = document.getElementById('notifications-list');

    // Clear the visual pane
    list.innerHTML = '';

    // Single query replaces 3 separate table fetches — RLS filters automatically
    const { data } = await supabase.from('user_notifications')
        .select('event_type, payload, event_time')
        .order('created_at', { ascending: false })
        .limit(20);

    if (data) data.reverse().forEach(evt => addNotification(formatNotification(evt)));

    // If still entirely empty after historical pull, show the empty state stub
    if (list.children.length === 0) {
        list.innerHTML = '<li id="notification-empty" class="notification-empty">No events received yet.</li>';
    } else {
        const emptyMsg = document.getElementById('notification-empty');
        if (emptyMsg) emptyMsg.style.display = 'none';
    }
}

let realtimeChannel = null;

// Format a notification row into a display string based on event_type
function formatNotification(evt) {
    const data = JSON.parse(evt.payload || '{}')
    switch (evt.event_type) {
        case 'identity.login': return `🚀 Flink mapped Login: ${evt.event_time}`
        case 'identity.signup': return `🎉 Flink mapped Signup: ${evt.event_time}`
        case 'identity.signout': return `👋 Flink mapped Signout: ${evt.event_time}`
        case 'user.message': return `💬 ${(data.email || '').split('@')[0]}: ${data.message || ''}`
        default: return `📌 ${evt.event_type}: ${evt.event_time}`
    }
}

// Real-Time Notifications — single subscription on user_notifications
function subscribeToEvents() {
    if (realtimeChannel) {
        supabase.removeChannel(realtimeChannel);
    }

    // RLS ensures we only receive our own identity events + broadcast messages + direct messages to us
    realtimeChannel = supabase.channel('user-events')
        .on(
            'postgres_changes',
            { event: 'INSERT', schema: 'public', table: 'user_notifications' },
            (payload) => addNotification(formatNotification(payload.new))
        )
        .subscribe();
}

function addNotification(message) {
    const list = document.getElementById('notifications-list');
    const emptyMsg = document.getElementById('notification-empty');
    if (emptyMsg) emptyMsg.style.display = 'none';

    const li = document.createElement('li');
    li.className = 'notification-item';
    li.innerText = message;

    list.prepend(li);

    // Cap memory at 50 events
    if (list.children.length > 50) {
        list.removeChild(list.lastChild);
    }
}

// Session Management
async function checkSession() {
    const { data: { session } } = await supabase.auth.getSession()

    if (session) {
        // User is logged in
        authView.classList.remove('active-view')
        homeView.classList.add('active-view')

        const email = session.user.email
        const username = email.split('@')[0]
        welcomeMessage.innerText = `Hello ${username}!`

        // Pre-fetch the latest history to prevent rapid-fire race conditions
        fetchHistory(session.user.id);

        // Subscribe to NEW Flink DB events exclusively for this user
        subscribeToEvents()

        // Load media data
        refreshCredits()
        refreshMediaBrowser()
    } else {
        // User is not logged in
        homeView.classList.remove('active-view')
        authView.classList.add('active-view')

        // Clean up socket
        if (realtimeChannel) {
            supabase.removeChannel(realtimeChannel)
            realtimeChannel = null
        }

        // Clear pane UI
        const list = document.getElementById('notifications-list');
        list.innerHTML = '<li id="notification-empty" class="notification-empty">No events received yet.</li>';

        // Reset form
        authForm.reset()
        hideAlert()
    }
}

// Listen for auth state changes globally
supabase.auth.onAuthStateChange((event, session) => {
    if (event === 'SIGNED_IN' || event === 'SIGNED_OUT') {
        checkSession()
    }
})

// Initialize
checkSession()
