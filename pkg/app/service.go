// Copyright 2022 The ILLA Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package app

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/illacloud/builder-backend/internal/repository"

	"go.uber.org/zap"
)

type AppService interface {
	CreateApp(app AppDto) (AppDto, error)
	UpdateApp(app AppDto) (AppDto, error)
	FetchAppByID(appID int) (AppDto, error)
	DeleteApp(appID int) error
	GetAllApps() ([]AppDto, error)
	DuplicateApp(appID, userID int, name string) (AppDto, error)
	ReleaseApp(appID int) (int, error)
	GetMegaData(appID, version int) (Editor, error)
}

type AppServiceImpl struct {
	logger              *zap.SugaredLogger
	appRepository       repository.AppRepository
	userRepository      repository.UserRepository
	kvstateRepository   repository.KVStateRepository
	treestateRepository repository.TreeStateRepository
	setstateRepository  repository.SetStateRepository
	actionRepository    repository.ActionRepository
}

var type_array = [22]string{"transformer", "restapi", "graphql", "redis", "mysql", "mariadb", "postgresql", "mongodb",
	"tidb", "elasticsearch", "s3", "smtp", "supabasedb", "firebase", "clickhouse", "mssql", "huggingface", "dynamodb",
	"snowflake", "couchdb", "hfendpoint", "oracle"}

type AppDto struct {
	ID              int         `json:"appId"` // generated by database primary key serial
	Name            string      `json:"appName" validate:"required"`
	ReleaseVersion  int         `json:"release_version"`  // release version used for mark the app release version.
	MainlineVersion int         `json:"mainline_version"` // mainline version keep the newest app version in database.
	CreatedBy       int         `json:"-" `
	CreatedAt       time.Time   `json:"-"`
	UpdatedBy       int         `json:"updatedBy"`
	UpdatedAt       time.Time   `json:"updatedAt"`
	AppActivity     AppActivity `json:"appActivity"`
}

type AppActivity struct {
	Modifier   string    `json:"modifier"`
	ModifiedAt time.Time `json:"modifiedAt"`
}

func NewAppDto() *AppDto {
	return &AppDto{}
}

func (appd *AppDto) ConstructByMap(data interface{}) {

	udata, ok := data.(map[string]interface{})
	if !ok {
		return
	}
	for k, v := range udata {
		switch k {
		case "id":
			idf, _ := v.(float64)
			appd.ID = int(idf)
		case "name":
			appd.Name, _ = v.(string)
		}
	}
}

func (appd *AppDto) ConstructWithID(id int) {
	appd.ID = id
}

func (appd *AppDto) ConstructWithUpdateBy(updateBy int) {
	appd.UpdatedBy = updateBy
}

type Editor struct {
	AppInfo               AppDto                 `json:"appInfo"`
	Actions               []Action               `json:"actions"`
	Components            *ComponentNode         `json:"components"`
	DependenciesState     map[string][]string    `json:"dependenciesState"`
	DragShadowState       map[string]interface{} `json:"dragShadowState"`
	DottedLineSquareState map[string]interface{} `json:"dottedLineSquareState"`
	DisplayNameState      []string               `json:"displayNameState"`
}

type Action struct {
	ID          int                    `json:"actionId"`
	App         int                    `json:"-"`
	Version     int                    `json:"-"`
	Resource    int                    `json:"resourceId,omitempty"`
	DisplayName string                 `json:"displayName"`
	Type        string                 `json:"actionType"`
	Template    map[string]interface{} `json:"content"`
	Transformer map[string]interface{} `json:"transformer"`
	TriggerMode string                 `json:"triggerMode"`
	CreatedAt   time.Time              `json:"createdAt,omitempty"`
	CreatedBy   int                    `json:"createdBy,omitempty"`
	UpdatedAt   time.Time              `json:"updatedAt,omitempty"`
	UpdatedBy   int                    `json:"updatedBy,omitempty"`
}

func NewAppServiceImpl(logger *zap.SugaredLogger, appRepository repository.AppRepository,
	userRepository repository.UserRepository, kvstateRepository repository.KVStateRepository,
	treestateRepository repository.TreeStateRepository, setstateRepository repository.SetStateRepository,
	actionRepository repository.ActionRepository) *AppServiceImpl {
	return &AppServiceImpl{
		logger:              logger,
		appRepository:       appRepository,
		userRepository:      userRepository,
		kvstateRepository:   kvstateRepository,
		treestateRepository: treestateRepository,
		setstateRepository:  setstateRepository,
		actionRepository:    actionRepository,
	}
}

func (impl *AppServiceImpl) CreateApp(app AppDto) (AppDto, error) {
	// init
	app.ReleaseVersion = 0 // the draft version will always be 0, so the release version and mainline version are 0 by default when app init.
	app.MainlineVersion = 0
	app.CreatedAt = time.Now().UTC()
	app.UpdatedAt = time.Now().UTC()
	id, err := impl.appRepository.Create(&repository.App{
		Name:            app.Name,
		ReleaseVersion:  app.ReleaseVersion,
		MainlineVersion: app.MainlineVersion,
		CreatedBy:       app.CreatedBy,
		CreatedAt:       app.CreatedAt,
		UpdatedBy:       app.UpdatedBy,
		UpdatedAt:       app.UpdatedAt,
	})
	if err != nil {
		return AppDto{}, err
	}
	app.ID = id
	userRecord, _ := impl.userRepository.RetrieveByID(app.CreatedBy)
	app.AppActivity.Modifier = userRecord.Nickname
	app.AppActivity.ModifiedAt = app.UpdatedAt // TODO: find last modified time in another record with version 0
	// create kv_states and tree_states for new app
	_ = impl.initialAllTypeTreeStates(app.ID, app.CreatedBy)
	return app, nil
}

func (impl *AppServiceImpl) initialAllTypeTreeStates(appID, user int) error {
	// create `root` component
	if _, err := impl.treestateRepository.Create(&repository.TreeState{
		StateType:          repository.TREE_STATE_TYPE_COMPONENTS,
		ParentNodeRefID:    0,
		ChildrenNodeRefIDs: "[]",
		AppRefID:           appID,
		Version:            repository.APP_EDIT_VERSION,
		Name:               "root",
		Content:            initialComponet,
		CreatedAt:          time.Now().UTC(),
		CreatedBy:          user,
		UpdatedAt:          time.Now().UTC(),
		UpdatedBy:          user,
	}); err != nil {
		return errors.New("initial tree state failed")
	}
	return nil
}

func (impl *AppServiceImpl) UpdateApp(app AppDto) (AppDto, error) {
	app.UpdatedAt = time.Now().UTC()
	if err := impl.appRepository.Update(&repository.App{
		ID:              app.ID,
		Name:            app.Name,
		ReleaseVersion:  app.ReleaseVersion,
		MainlineVersion: app.MainlineVersion,
		UpdatedBy:       app.UpdatedBy,
		UpdatedAt:       app.UpdatedAt,
	}); err != nil {
		return AppDto{}, err
	}
	userRecord, _ := impl.userRepository.RetrieveByID(app.UpdatedBy)
	app.AppActivity.Modifier = userRecord.Nickname
	app.AppActivity.ModifiedAt = app.UpdatedAt // TODO: find last modified time in another record with version 0
	return app, nil
}

// call this method when action (over HTTP) and state (over websocket) changed
func (impl *AppServiceImpl) UpdateAppModifyTime(app *AppDto) error {
	app.UpdatedAt = time.Now().UTC()
	if err := impl.appRepository.UpdateUpdatedAt(&repository.App{
		ID:        app.ID,
		UpdatedBy: app.UpdatedBy,
		UpdatedAt: app.UpdatedAt,
	}); err != nil {
		return err
	}
	return nil
}

func (impl *AppServiceImpl) FetchAppByID(appID int) (AppDto, error) {
	app, err := impl.appRepository.RetrieveAppByID(appID)
	if err != nil {
		return AppDto{}, err
	}
	res := AppDto{
		ID:              app.ID,
		Name:            app.Name,
		ReleaseVersion:  app.ReleaseVersion,
		MainlineVersion: app.MainlineVersion,
		UpdatedBy:       app.UpdatedBy,
		UpdatedAt:       app.UpdatedAt,
	}
	return res, nil
}

func (impl *AppServiceImpl) DeleteApp(appID int) error { // TODO: maybe need transaction
	_ = impl.treestateRepository.DeleteAllTypeTreeStatesByApp(appID)
	_ = impl.kvstateRepository.DeleteAllTypeKVStatesByApp(appID)
	_ = impl.actionRepository.DeleteActionsByApp(appID)
	_ = impl.setstateRepository.DeleteAllTypeSetStatesByApp(appID)
	return impl.appRepository.Delete(appID)
}

func (impl *AppServiceImpl) GetAllApps() ([]AppDto, error) {
	res, err := impl.appRepository.RetrieveAllByUpdatedTime()
	if err != nil {
		return nil, err
	}
	resDtoSlice := make([]AppDto, 0, len(res))
	for _, value := range res {
		userRecord, _ := impl.userRepository.RetrieveByID(value.UpdatedBy)
		resDtoSlice = append(resDtoSlice, AppDto{
			ID:              value.ID,
			Name:            value.Name,
			ReleaseVersion:  value.ReleaseVersion,
			MainlineVersion: value.MainlineVersion,
			UpdatedAt:       value.UpdatedAt,
			UpdatedBy:       value.UpdatedBy,
			AppActivity: AppActivity{
				Modifier:   userRecord.Nickname,
				ModifiedAt: value.UpdatedAt,
			},
		})
	}
	return resDtoSlice, nil
}

func (impl *AppServiceImpl) DuplicateApp(appID, userID int, name string) (AppDto, error) {
	appA, err := impl.appRepository.RetrieveAppByID(appID)
	if err != nil {
		return AppDto{}, err
	}
	appA.ReleaseVersion = 0 // the draft version will always be 0, so the release version and mainline version are 0 by default when app init.
	appA.MainlineVersion = 0
	appA.CreatedAt = time.Now().UTC()
	appA.UpdatedAt = time.Now().UTC()
	appA.CreatedBy = userID
	appA.UpdatedBy = userID
	id, err := impl.appRepository.Create(&repository.App{
		Name:            name,
		ReleaseVersion:  appA.ReleaseVersion,
		MainlineVersion: appA.MainlineVersion,
		CreatedBy:       appA.CreatedBy,
		CreatedAt:       appA.CreatedAt,
		UpdatedBy:       appA.UpdatedBy,
		UpdatedAt:       appA.UpdatedAt,
	})
	if err != nil {
		return AppDto{}, err
	}
	userRecord, _ := impl.userRepository.RetrieveByID(userID)
	appB := AppDto{
		ID:              id,
		Name:            name,
		ReleaseVersion:  appA.ReleaseVersion,
		MainlineVersion: appA.MainlineVersion,
		CreatedBy:       appA.CreatedBy,
		CreatedAt:       appA.CreatedAt,
		UpdatedBy:       appA.UpdatedBy,
		UpdatedAt:       appA.UpdatedAt,
		AppActivity: AppActivity{
			Modifier:   userRecord.Nickname,
			ModifiedAt: userRecord.UpdatedAt,
		},
	}
	_ = impl.copyAllTreeState(appID, appB.ID, userID)
	_ = impl.copyAllKVState(appID, appB.ID, userID)
	_ = impl.copyAllSetState(appID, appB.ID, userID)
	_ = impl.copyActions(appID, appB.ID, userID)
	return appB, nil
}

func (impl *AppServiceImpl) copyAllTreeState(appA, appB, user int) error {
	// get edit version K-V state from database
	treestates, err := impl.treestateRepository.RetrieveAllTypeTreeStatesByApp(appA, repository.APP_EDIT_VERSION)
	if err != nil {
		return err
	}
	// update some fields
	indexIDMap := map[int]int{}
	releaseIDMap := map[int]int{}
	for serial, _ := range treestates {
		indexIDMap[serial] = treestates[serial].ID
		treestates[serial].ID = 0
		treestates[serial].AppRefID = appB
		treestates[serial].Version = repository.APP_EDIT_VERSION
		treestates[serial].CreatedBy = user
		treestates[serial].CreatedAt = time.Now().UTC()
		treestates[serial].UpdatedBy = user
		treestates[serial].UpdatedAt = time.Now().UTC()
	}
	// and put them to the database as duplicate
	for i, treestate := range treestates {
		id, err := impl.treestateRepository.Create(treestate)
		if err != nil {
			return err
		}
		oldID := indexIDMap[i]
		releaseIDMap[oldID] = id
	}

	for _, treestate := range treestates {
		treestate.ChildrenNodeRefIDs = convertLink(treestate.ChildrenNodeRefIDs, releaseIDMap)
		treestate.ParentNodeRefID = releaseIDMap[treestate.ParentNodeRefID]
		if err := impl.treestateRepository.Update(treestate); err != nil {
			return err
		}
	}

	return nil
}

func (impl *AppServiceImpl) copyAllKVState(appA, appB, user int) error {
	// get edit version K-V state from database
	kvstates, err := impl.kvstateRepository.RetrieveAllTypeKVStatesByApp(appA, repository.APP_EDIT_VERSION)
	if err != nil {
		return err
	}
	// update some fields
	for serial, _ := range kvstates {
		kvstates[serial].ID = 0
		kvstates[serial].AppRefID = appB
		kvstates[serial].Version = repository.APP_EDIT_VERSION
		kvstates[serial].CreatedBy = user
		kvstates[serial].CreatedAt = time.Now().UTC()
		kvstates[serial].UpdatedBy = user
		kvstates[serial].UpdatedAt = time.Now().UTC()
	}
	// and put them to the database as duplicate
	for _, kvstate := range kvstates {
		if err := impl.kvstateRepository.Create(kvstate); err != nil {
			return err
		}
	}
	return nil
}

func (impl *AppServiceImpl) copyAllSetState(appA, appB, user int) error {
	setstates, err := impl.setstateRepository.RetrieveSetStatesByApp(appA, repository.SET_STATE_TYPE_DISPLAY_NAME, repository.APP_EDIT_VERSION)
	if err != nil {
		return err
	}
	// update some fields
	for serial, _ := range setstates {
		setstates[serial].ID = 0
		setstates[serial].AppRefID = appB
		setstates[serial].Version = repository.APP_EDIT_VERSION
		setstates[serial].CreatedBy = user
		setstates[serial].CreatedAt = time.Now().UTC()
		setstates[serial].UpdatedBy = user
		setstates[serial].UpdatedAt = time.Now().UTC()
	}
	// and put them to the database as duplicate
	for _, setstate := range setstates {
		if err := impl.setstateRepository.Create(setstate); err != nil {
			return err
		}
	}
	return nil
}

func (impl *AppServiceImpl) copyActions(appA, appB, user int) error {
	// get edit version K-V state from database
	actions, err := impl.actionRepository.RetrieveActionsByAppVersion(appA, repository.APP_EDIT_VERSION)
	if err != nil {
		return err
	}
	// update some fields
	for serial, _ := range actions {
		actions[serial].ID = 0
		actions[serial].App = appB
		actions[serial].Version = repository.APP_EDIT_VERSION
		actions[serial].CreatedBy = user
		actions[serial].CreatedAt = time.Now().UTC()
		actions[serial].UpdatedBy = user
		actions[serial].UpdatedAt = time.Now().UTC()
	}
	// and put them to the database as duplicate
	for _, action := range actions {
		if _, err := impl.actionRepository.Create(action); err != nil {
			return err
		}
	}
	return nil
}

func (impl *AppServiceImpl) ReleaseApp(appID int) (int, error) {
	app, err := impl.appRepository.RetrieveAppByID(appID)
	if err != nil {
		return -1, nil
	}
	app.MainlineVersion += 1
	app.ReleaseVersion = app.MainlineVersion
	_ = impl.releaseTreeStateByApp(AppDto{ID: appID, MainlineVersion: app.MainlineVersion})
	_ = impl.releaseKVStateByApp(AppDto{ID: appID, MainlineVersion: app.MainlineVersion})
	_ = impl.releaseSetStateByApp(AppDto{ID: appID, MainlineVersion: app.MainlineVersion})
	_ = impl.releaseActionsByApp(AppDto{ID: appID, MainlineVersion: app.MainlineVersion})
	if err := impl.appRepository.Update(app); err != nil {
		return -1, nil
	}

	return app.ReleaseVersion, nil
}

func (impl *AppServiceImpl) releaseTreeStateByApp(app AppDto) error {
	// get edit version K-V state from database
	treestates, err := impl.treestateRepository.RetrieveAllTypeTreeStatesByApp(app.ID, repository.APP_EDIT_VERSION)
	if err != nil {
		return err
	}
	indexIDMap := map[int]int{}
	releaseIDMap := map[int]int{}
	// set version as mainline version
	for serial, _ := range treestates {
		indexIDMap[serial] = treestates[serial].ID
		treestates[serial].ID = 0
		treestates[serial].Version = app.MainlineVersion
	}
	// and put them to the database as duplicate
	for i, treestate := range treestates {
		id, err := impl.treestateRepository.Create(treestate)
		if err != nil {
			return err
		}
		oldID := indexIDMap[i]
		releaseIDMap[oldID] = id
	}
	for _, treestate := range treestates {
		treestate.ChildrenNodeRefIDs = convertLink(treestate.ChildrenNodeRefIDs, releaseIDMap)
		treestate.ParentNodeRefID = releaseIDMap[treestate.ParentNodeRefID]
		if err := impl.treestateRepository.Update(treestate); err != nil {
			return err
		}
	}

	return nil
}

func (impl *AppServiceImpl) releaseKVStateByApp(app AppDto) error {
	// get edit version K-V state from database
	kvstates, err := impl.kvstateRepository.RetrieveAllTypeKVStatesByApp(app.ID, repository.APP_EDIT_VERSION)
	if err != nil {
		return err
	}
	// set version as mainline version
	for serial, _ := range kvstates {
		kvstates[serial].ID = 0
		kvstates[serial].Version = app.MainlineVersion
	}
	// and put them to the database as duplicate
	for _, kvstate := range kvstates {
		if err := impl.kvstateRepository.Create(kvstate); err != nil {
			return err
		}
	}
	return nil
}

func (impl *AppServiceImpl) releaseSetStateByApp(app AppDto) error {
	setstates, err := impl.setstateRepository.RetrieveSetStatesByApp(app.ID, repository.SET_STATE_TYPE_DISPLAY_NAME, repository.APP_EDIT_VERSION)
	if err != nil {
		return err
	}
	// update some fields
	for serial, _ := range setstates {
		setstates[serial].ID = 0
		setstates[serial].Version = app.MainlineVersion
	}
	// and put them to the database as duplicate
	for _, setstate := range setstates {
		if err := impl.setstateRepository.Create(setstate); err != nil {
			return err
		}
	}
	return nil
}

func (impl *AppServiceImpl) releaseActionsByApp(app AppDto) error {
	// get edit version K-V state from database
	actions, err := impl.actionRepository.RetrieveActionsByAppVersion(app.ID, repository.APP_EDIT_VERSION)
	if err != nil {
		return err
	}
	// set version as mainline version
	for serial, _ := range actions {
		actions[serial].ID = 0
		actions[serial].Version = app.MainlineVersion
	}
	// and put them to the database as duplicate
	for _, action := range actions {
		if _, err := impl.actionRepository.Create(action); err != nil {
			return err
		}
	}
	return nil
}

func (impl *AppServiceImpl) GetMegaData(appID, version int) (Editor, error) {
	editor, err := impl.fetchEditor(appID, version)
	if err != nil {
		return Editor{}, err
	}
	return editor, nil
}

func (impl *AppServiceImpl) fetchEditor(appID int, version int) (Editor, error) {
	app, err := impl.appRepository.RetrieveAppByID(appID)
	if err != nil {
		return Editor{}, err
	}
	if app.ID == 0 || version > app.MainlineVersion {
		return Editor{}, errors.New("content not found")
	}
	userRecord, _ := impl.userRepository.RetrieveByID(app.UpdatedBy)

	res := Editor{}
	res.AppInfo = AppDto{
		ID:              app.ID,
		Name:            app.Name,
		ReleaseVersion:  app.ReleaseVersion,
		MainlineVersion: app.MainlineVersion,
		UpdatedAt:       app.UpdatedAt,
		UpdatedBy:       app.UpdatedBy,
		AppActivity: AppActivity{
			Modifier:   userRecord.Nickname,
			ModifiedAt: userRecord.UpdatedAt,
		},
	}
	res.Actions, _ = impl.formatActions(appID, version)
	res.Components, _ = impl.formatComponents(appID, version)
	res.DependenciesState, _ = impl.formatDependenciesState(appID, version)
	res.DragShadowState, _ = impl.formatDragShadowState(appID, version)
	res.DottedLineSquareState, _ = impl.formatDottedLineSquareState(appID, version)
	res.DisplayNameState, _ = impl.formatDisplayNameState(appID, version)

	return res, nil
}

func (impl *AppServiceImpl) formatActions(appID, version int) ([]Action, error) {
	res, err := impl.actionRepository.RetrieveActionsByAppVersion(appID, version)
	if err != nil {
		return nil, err
	}

	resSlice := make([]Action, 0, len(res))
	for _, value := range res {
		resSlice = append(resSlice, Action{
			ID:          value.ID,
			Resource:    value.Resource,
			DisplayName: value.Name,
			Type:        type_array[value.Type],
			Transformer: value.Transformer,
			TriggerMode: value.TriggerMode,
			Template:    value.Template,
			CreatedBy:   value.CreatedBy,
			CreatedAt:   value.CreatedAt,
			UpdatedBy:   value.UpdatedBy,
			UpdatedAt:   value.UpdatedAt,
		})
	}
	return resSlice, nil
}

func (impl *AppServiceImpl) formatComponents(appID, version int) (*ComponentNode, error) {
	res, err := impl.treestateRepository.RetrieveTreeStatesByApp(appID, repository.TREE_STATE_TYPE_COMPONENTS, version)
	if err != nil {
		return nil, err
	}

	if len(res) == 0 {
		return nil, errors.New("no component")
	}

	tempMap := map[int]*repository.TreeState{}
	root := &repository.TreeState{}
	for _, component := range res {
		if component.Name == repository.TREE_STATE_SUMMIT_NAME {
			root = component
		}
		tempMap[component.ID] = component
	}
	resNode, _ := buildComponentTree(root, tempMap, nil)

	return resNode, nil
}

func (impl *AppServiceImpl) formatDependenciesState(appID, version int) (map[string][]string, error) {
	res, err := impl.kvstateRepository.RetrieveKVStatesByApp(appID, repository.KV_STATE_TYPE_DEPENDENCIES, version)
	if err != nil {
		return nil, err
	}

	resMap := map[string][]string{}

	if len(res) == 0 {
		return resMap, nil
	}

	for _, dependency := range res {
		var revMsg []string
		json.Unmarshal([]byte(dependency.Value), &revMsg)
		resMap[dependency.Key] = revMsg // value convert to []string
	}

	return resMap, nil
}

func (impl *AppServiceImpl) formatDragShadowState(appID, version int) (map[string]interface{}, error) {
	res, err := impl.kvstateRepository.RetrieveKVStatesByApp(appID, repository.KV_STATE_TYPE_DRAG_SHADOW, version)
	if err != nil {
		return nil, err
	}

	resMap := map[string]interface{}{}

	if len(res) == 0 {
		return resMap, nil
	}

	for _, shadow := range res {
		var revMsg map[string]interface{}
		json.Unmarshal([]byte(shadow.Value), &revMsg)
		resMap[shadow.Key] = revMsg
	}

	return resMap, nil
}

func (impl *AppServiceImpl) formatDottedLineSquareState(appID, version int) (map[string]interface{}, error) {
	res, err := impl.kvstateRepository.RetrieveKVStatesByApp(appID, repository.KV_STATE_TYPE_DOTTED_LINE_SQUARE, version)
	if err != nil {
		return nil, err
	}

	resMap := map[string]interface{}{}

	if len(res) == 0 {
		return resMap, nil
	}

	for _, line := range res {
		var revMsg map[string]interface{}
		json.Unmarshal([]byte(line.Value), &revMsg)
		resMap[line.Key] = line.Value
	}

	return resMap, nil
}

func (impl *AppServiceImpl) formatDisplayNameState(appID, version int) ([]string, error) {
	res, err := impl.setstateRepository.RetrieveSetStatesByApp(appID, repository.SET_STATE_TYPE_DISPLAY_NAME, version)
	if err != nil {
		return nil, err
	}

	resSlice := make([]string, 0, len(res))
	if len(res) == 0 {
		return resSlice, nil
	}

	for _, displayName := range res {
		resSlice = append(resSlice, displayName.Value)
	}

	return resSlice, nil
}

func convertLink(ref string, idMap map[int]int) string {
	// convert string to []int
	var oldIDs []int
	if err := json.Unmarshal([]byte(ref), &oldIDs); err != nil {
		return ""
	}
	// map old id to new id
	newIDs := make([]int, 0, len(oldIDs))
	for _, oldID := range oldIDs {
		newIDs = append(newIDs, idMap[oldID])
	}
	// convert []int to string
	idsjsonb, err := json.Marshal(newIDs)
	if err != nil {
		return ""
	}
	// return result
	return string(idsjsonb)
}
