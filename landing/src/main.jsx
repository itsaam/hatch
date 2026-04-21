import React from 'react'
import ReactDOM from 'react-dom/client'
import App from './App.jsx'
import AdminPage from './admin/AdminPage.jsx'
import './styles/tokens.css'
import './styles/global.css'

const isApp = typeof window !== 'undefined' && window.location.pathname.startsWith('/app')

ReactDOM.createRoot(document.getElementById('root')).render(
  isApp ? <AdminPage /> : <App />,
)
