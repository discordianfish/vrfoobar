package main

import (
	"fmt"
	"log"
	"math/rand"
	"runtime"

	"github.com/go-gl/glfw/v3.1/glfw"
	"github.com/go-gl/mathgl/mgl32"
	"github.com/tbogdala/fizzle"
	graphics "github.com/tbogdala/fizzle/graphicsprovider"
	"github.com/tbogdala/fizzle/graphicsprovider/opengl"
	"github.com/tbogdala/fizzle/input/glfwinput"
	"github.com/tbogdala/fizzle/renderer/forward"
	"github.com/tbogdala/openvr-go"
	"github.com/tbogdala/openvr-go/util/fizzlevr"
)

func init() {
	runtime.LockOSThread()
}

func main() {
	app, err := newStarfield()
	if err != nil {
		log.Fatal(err)
	}
	app.run()
}

// Starfield
type starfield struct {
	*window
	*vrSystem
	gl                *opengl.GraphicsImpl
	height            uint32
	width             uint32
	eyeLeft           *fizzlevr.EyeFramebuffer
	eyeRight          *fizzlevr.EyeFramebuffer
	eyeTransforms     *openvr.EyeTransforms
	distortionLens    *fizzlevr.DistortionLens
	kbModel           *glfwinput.KeyboardModel
	vrCompositor      *openvr.Compositor
	hmdPose           mgl32.Mat4
	hmdLoc            mgl32.Vec3
	renderer          *forward.ForwardRenderer
	deviceRenderables *fizzlevr.DeviceRenderables
	renderables       []*fizzle.Renderable
}

func newStarfield() (*starfield, error) {
	win, err := newWindow(1280, 720, "starfield")
	if err != nil {
		return nil, err
	}

	kbModel := glfwinput.NewKeyboardModel(win.Window)
	kbModel.BindTrigger(glfw.KeyEscape, func() { win.SetShouldClose(true) })
	kbModel.SetupCallbacks()

	vr, err := newVR()
	if err != nil {
		return nil, err
	}

	gl, err := opengl.InitOpenGL()
	if err != nil {
		return nil, err
	}
	fizzle.SetGraphics(gl)

	sb, sm, sl, err := newShaders()
	if err != nil {
		return nil, err
	}

	// create a new renderer
	renderer := forward.NewForwardRenderer(gl)
	width, height := vr.GetRecommendedRenderTargetSize()
	renderer.ChangeResolution(int32(width), int32(width))

	// put a light in there
	light := renderer.NewDirectionalLight(mgl32.Vec3{1.0, -0.5, -1.0})
	light.DiffuseIntensity = 0.70
	light.SpecularIntensity = 0.10
	light.AmbientIntensity = 0.3
	renderer.ActiveLights[0] = light

	redMaterial := fizzle.NewMaterial()
	redMaterial.Shader = sb
	redMaterial.DiffuseColor = mgl32.Vec4{1.0, 1.0, 1.0, 1.0}
	redMaterial.Shininess = 10

	stars := 1000
	maxX := 10.0
	maxY := 10.0
	maxZ := 10.0
	renderables := make([]*fizzle.Renderable, stars)
	for i := 0; i < stars; i++ {
		x := (rand.Float64() * maxX) - maxX/2
		y := (rand.Float64() * maxY) - maxY/2
		z := (rand.Float64() * maxZ) - maxZ/2
		renderables[i] = fizzle.CreateSphere(0.1, 5, 5)
		renderables[i].Material = redMaterial
		renderables[i].Location = mgl32.Vec3{float32(x), float32(y), float32(z)}
	}

	// FIXME: What exactly do those mean?
	eyeTransforms := vr.GetEyeTransforms(0.1, 30.0)
	eyeLeft, eyeRight := fizzlevr.CreateStereoRenderTargets(width, height)
	distortionLens := fizzlevr.CreateDistortionLens(vr.System, sl, eyeLeft, eyeRight)

	deviceRenderables, err := fizzlevr.CreateDeviceRenderables(vr.System, sm)
	if err != nil {
		return nil, err
	}

	vrCompositor, err := openvr.GetCompositor()
	if err != nil {
		return nil, err
	}

	app := &starfield{
		window:            win,
		vrSystem:          vr,
		gl:                gl,
		height:            height,
		width:             width,
		eyeLeft:           eyeLeft,
		eyeRight:          eyeRight,
		eyeTransforms:     eyeTransforms,
		distortionLens:    distortionLens,
		kbModel:           kbModel,
		vrCompositor:      vrCompositor,
		renderables:       renderables,
		deviceRenderables: deviceRenderables,
		renderer:          renderer,
	}
	return app, nil
}

func (a *starfield) run() {
	for !a.window.ShouldClose() {
		a.handleInput()
		a.renderFrame()
	}
}

func (a *starfield) handleInput() {
	// advise GLFW to poll for input. without this the window appears to hang.
	glfw.PollEvents()

	// handle any keyboard input
	a.kbModel.CheckKeyPresses()

	var event openvr.VREvent
	for a.vrSystem.PollNextEvent(&event) {
		switch event.EventType {
		case openvr.VREventTrackedDeviceActivated:
			fmt.Printf("Device %d attached.\n", event.TrackedDeviceIndex)
		case openvr.VREventTrackedDeviceDeactivated:
			fmt.Printf("Device %d detached.\n", event.TrackedDeviceIndex)
		case openvr.VREventTrackedDeviceUpdated:
			fmt.Printf("Device %d updated.\n", event.TrackedDeviceIndex)
		}
	}
}

func (a *starfield) renderFrame() {
	a.renderStereoTargets()

	// draw the framebuffers to the window
	a.distortionLens.Render(int32(a.width), int32(a.height))

	// send the framebuffer textures out to the compositor for rendering to the HMD
	a.vrCompositor.Submit(openvr.EyeLeft, uint32(a.eyeLeft.ResolveTexture))
	a.vrCompositor.Submit(openvr.EyeRight, uint32(a.eyeRight.ResolveTexture))

	// draw the screen
	a.window.SwapBuffers()
	// WaitGetPoses is used as a sync point in the OpenVR API. This is on a timer to keep 90fps, so
	// the OpenVR gives you that much time to draw a frame. By calling WaitGetPoses() you wait the
	// remaining amount of time. If you only used 1ms it will wait 10ms here. If you used 5ms it will wait 6ms.
	// (approx.)
	a.vrCompositor.WaitGetPoses(false)
	if a.vrCompositor.IsPoseValid(openvr.TrackedDeviceIndexHmd) {
		pose := a.vrCompositor.GetRenderPose(openvr.TrackedDeviceIndexHmd)
		a.hmdPose = mgl32.Mat4(openvr.Mat34ToMat4(&pose.DeviceToAbsoluteTracking)).Inv()

		// FIXME: this is probably broken.
		a.hmdLoc[0] = pose.DeviceToAbsoluteTracking[9]
		a.hmdLoc[1] = pose.DeviceToAbsoluteTracking[10]
		a.hmdLoc[2] = pose.DeviceToAbsoluteTracking[11]
	}
}

func (a *starfield) renderStereoTargets() {
	a.gl.Enable(graphics.CULL_FACE)
	a.gl.ClearColor(0.15, 0.15, 0.18, 1.0) // nice background color, but not black

	// left eye
	a.gl.Enable(graphics.MULTISAMPLE)
	a.gl.BindFramebuffer(graphics.FRAMEBUFFER, a.eyeLeft.RenderFramebuffer)
	a.gl.Viewport(0, 0, int32(a.width), int32(a.height))
	a.renderScene(openvr.EyeLeft)
	a.gl.BindFramebuffer(graphics.FRAMEBUFFER, 0)
	a.gl.Disable(graphics.MULTISAMPLE)

	a.gl.BindFramebuffer(graphics.READ_FRAMEBUFFER, a.eyeLeft.RenderFramebuffer)
	a.gl.BindFramebuffer(graphics.DRAW_FRAMEBUFFER, a.eyeLeft.ResolveFramebuffer)
	a.gl.BlitFramebuffer(0, 0, int32(a.width), int32(a.height), 0, 0, int32(a.width), int32(a.height), graphics.COLOR_BUFFER_BIT, graphics.LINEAR)
	a.gl.BindFramebuffer(graphics.READ_FRAMEBUFFER, 0)
	a.gl.BindFramebuffer(graphics.DRAW_FRAMEBUFFER, 0)

	// right eye
	a.gl.Enable(graphics.MULTISAMPLE)
	a.gl.BindFramebuffer(graphics.FRAMEBUFFER, a.eyeRight.RenderFramebuffer)
	a.gl.Viewport(0, 0, int32(a.width), int32(a.height))
	a.renderScene(openvr.EyeRight)
	a.gl.BindFramebuffer(graphics.FRAMEBUFFER, 0)
	a.gl.Disable(graphics.MULTISAMPLE)

	a.gl.BindFramebuffer(graphics.READ_FRAMEBUFFER, a.eyeRight.RenderFramebuffer)
	a.gl.BindFramebuffer(graphics.DRAW_FRAMEBUFFER, a.eyeRight.ResolveFramebuffer)
	a.gl.BlitFramebuffer(0, 0, int32(a.width), int32(a.height), 0, 0, int32(a.width), int32(a.height), graphics.COLOR_BUFFER_BIT, graphics.LINEAR)
	a.gl.BindFramebuffer(graphics.READ_FRAMEBUFFER, 0)
	a.gl.BindFramebuffer(graphics.DRAW_FRAMEBUFFER, 0)
}

func (a *starfield) renderScene(eye int) {
	a.gl.Clear(graphics.COLOR_BUFFER_BIT | graphics.DEPTH_BUFFER_BIT)
	a.gl.Enable(graphics.DEPTH_TEST)

	var perspective, view mgl32.Mat4
	var camera FixedCamera
	if eye == openvr.EyeLeft {
		view = a.eyeTransforms.PositionLeft.Mul4(a.hmdPose)
		perspective = a.eyeTransforms.ProjectionLeft
		camera.View = view
		camera.Position = a.hmdLoc
	} else {
		view = a.eyeTransforms.PositionRight.Mul4(a.hmdPose)
		perspective = a.eyeTransforms.ProjectionRight
		camera.View = view
		camera.Position = a.hmdLoc
	}

	for _, obj := range a.renderables {
		a.renderer.DrawRenderable(obj, nil, perspective, view, camera)
	}

	// now draw any devices that get rendered into the scene
	a.deviceRenderables.RenderDevices(a.vrCompositor, perspective, view, camera)
}

// window
type window struct {
	*glfw.Window
	width  int
	height int
	title  string
}

func newWindow(width, height int, title string) (*window, error) {
	if err := glfw.Init(); err != nil {
		return nil, err
	}
	for hint, value := range map[glfw.Hint]int{
		glfw.Samples:                 4,
		glfw.ContextVersionMajor:     3,
		glfw.ContextVersionMinor:     3,
		glfw.OpenGLForwardCompatible: glfw.True,
		glfw.OpenGLProfile:           glfw.OpenGLCoreProfile,
	} {
		glfw.WindowHint(hint, value)
	}
	glwin, err := glfw.CreateWindow(width, height, title, nil, nil)
	if err != nil {
		return nil, err
	}

	win := &window{
		Window: glwin,
		width:  width,
		height: height,
	}
	glwin.SetSizeCallback(func(w *glfw.Window, width int, height int) {
		win.width = width
		win.height = height
	})
	win.MakeContextCurrent()
	glfw.SwapInterval(0) // Disable v-sync
	return win, nil
}

type vrSystem struct {
	*openvr.System
}

// vrSystem
func newVR() (*vrSystem, error) {
	vrs, err := openvr.Init()
	if err != nil {
		return nil, err
	}
	// FIXME: Example checks this. Necessary?
	if vrs == nil {
		panic("BUG")
	}
	vr := &vrSystem{System: vrs}

	name, err := vr.deviceProperty(openvr.TrackedDeviceIndexHmd, openvr.PropTrackingSystemNameString)
	if err != nil {
		return nil, err
	}

	dsn, err := vr.deviceProperty(openvr.TrackedDeviceIndexHmd, openvr.PropSerialNumberString)
	if err != nil {
		return nil, err
	}
	fmt.Printf("Connected to %s %s\n", name, dsn)
	return vr, nil
}

func (vr *vrSystem) deviceProperty(device, property uint) (string, error) {
	val, status := vr.GetStringTrackedDeviceProperty(int(openvr.TrackedDeviceIndexHmd), openvr.PropTrackingSystemNameString)
	if status != openvr.TrackedPropSuccess {
		return "", fmt.Errorf("Couldn't get property %d from device %d", property, device)
	}
	return val, nil
}

func newShaders() (*fizzle.RenderShader, *fizzle.RenderShader, *fizzle.RenderShader, error) {
	// basic shader
	sb, err := forward.CreateBasicShader()
	if err != nil {
		return nil, nil, nil, err
	}

	// For models of connected devices
	sr, err := fizzle.LoadShaderProgram(openvr.ShaderRenderModelV, openvr.ShaderRenderModelF, nil)
	if err != nil {
		return nil, nil, nil, err
	}
	ls, err := fizzle.LoadShaderProgram(openvr.ShaderLensDistortionV, openvr.ShaderLensDistortionF, nil)
	if err != nil {
		return nil, nil, nil, err
	}
	return sb, sr, ls, nil
}

type FixedCamera struct {
	View     mgl32.Mat4
	Position mgl32.Vec3
}

func (c FixedCamera) GetViewMatrix() mgl32.Mat4 {
	return c.View
}
func (c FixedCamera) GetPosition() mgl32.Vec3 {
	return c.Position
}
