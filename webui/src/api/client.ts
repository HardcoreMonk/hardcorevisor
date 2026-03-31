import axios from 'axios'

// API 클라이언트 설정
// VITE_API_URL 환경변수 또는 Vite 프록시를 통해 Controller에 연결
const apiClient = axios.create({
  baseURL: import.meta.env.VITE_API_URL || '',
  timeout: 10000,
  headers: {
    'Content-Type': 'application/json',
  },
})

// 요청 인터셉터: localStorage에서 JWT 토큰을 읽어 Authorization 헤더에 설정
apiClient.interceptors.request.use((config) => {
  const token = localStorage.getItem('hcv_token')
  if (token) {
    config.headers.Authorization = `Bearer ${token}`
  }
  return config
})

// 응답 인터셉터: 401 시 토큰 제거 및 로그인 페이지로 리다이렉트
apiClient.interceptors.response.use(
  (response) => response,
  (error) => {
    if (error.response?.status === 401) {
      localStorage.removeItem('hcv_token')
      window.location.href = '/login'
    }
    return Promise.reject(error)
  },
)

export default apiClient
