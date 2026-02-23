import './style.css'
import { createClient } from '@supabase/supabase-js'

const supabaseUrl = import.meta.env.VITE_SUPABASE_URL || 'http://127.0.0.1:54321'
const supabaseAnonKey = import.meta.env.VITE_SUPABASE_ANON_KEY
const apiGatewayUrl = import.meta.env.VITE_API_GATEWAY_URL || 'http://localhost:8080'

if (!supabaseAnonKey) {
    console.error("VITE_SUPABASE_ANON_KEY is missing. Please create a .env file locally with the key.");
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

let isSignupMode = false

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
        case 'identity.login':   return `🚀 Flink mapped Login: ${evt.event_time}`
        case 'identity.signup':  return `🎉 Flink mapped Signup: ${evt.event_time}`
        case 'identity.signout': return `👋 Flink mapped Signout: ${evt.event_time}`
        case 'user.message':     return `💬 ${(data.email || '').split('@')[0]}: ${data.message || ''}`
        default:                 return `📌 ${evt.event_type}: ${evt.event_time}`
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
