package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"github.com/360EntSecGroup-Skylar/excelize"
	log "github.com/sirupsen/logrus"
	"io/ioutil"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Root         string
	Txt          string
	JSON         string
	Lua          string
	FieldLine    int    // 字段key开始行
	DataLine     int    // 有效配置开始行
	TypeLine     int    // 类型配置开始行
	Comma        string // txt分隔符,默认是制表符
	Comment      string // excel注释符
	Linefeed     string // txt换行符
	UseSheetName bool   // 使用工作表名为文件输出名
}

var (
	ch        = make(chan string)
	fileCount = 0
	config    = Config{}
	fileList  = make([]interface{}, 0)
)

func main() {
	log.SetFormatter(&log.TextFormatter{ForceColors: true, FullTimestamp: true})

	startTime := time.Now().UnixNano()

	c := flag.String("C", "./config.json", "配置文件路径")
	flag.Parse()

	// 读取json配置
	data, err := ioutil.ReadFile(*c)
	if err != nil {
		log.Fatalf("%v\n", err)
		return
	}

	if err = json.Unmarshal(data, &config); err != nil {
		log.Fatalf("%v\n", err)
		return
	}

	// 创建输出路径
	outList := []string{config.Txt, config.Lua, config.JSON}
	for _, v := range outList {
		if v != "" {
			err = createDir(v)
			if err != nil {
				return
			}
		}
	}

	// 遍历打印所有的文件名
	filepath.Walk(config.Root, walkFunc)
	count := 0
	for {
		sheetName, open := <-ch
		if !open {
			break
		}

		if sheetName != "" {
			fileList = append(fileList, sheetName)
		}

		count++
		if count == fileCount {
			writeFileList()
			break
		}
	}

	endTime := time.Now().UnixNano()
	log.Infof("总耗时:%v毫秒\n", (endTime-startTime)/1000000)
	time.Sleep(time.Second)
}

// 写文件列表
func writeFileList() {
	data := make(map[string]interface{})

	sortList := make([]string, len(fileList))
	for i, v := range fileList {
		sortList[i] = v.(string)
	}
	sort.Strings(sortList)
	data["fileList"] = sortList

	if config.Txt != "" {
		writeJSON(config.Txt, "fileList", &data)
	}

	if config.JSON != "" {
		writeJSON(config.JSON, "fileList", &data)
	}

	if config.Lua != "" {
		writeLuaTable(config.Lua, "fileList", &data)
	}
}

// 创建文件夹
func createDir(dir string) error {
	exist, err := pathExists(dir)
	if err != nil {
		log.Fatalf("get dir error![%v]\n", err)
		return err
	}

	if !exist {
		if err := os.MkdirAll(dir, os.ModePerm); err != nil {
			log.Fatalf("mkdir failed![%v]\n", err)
		} else {
			log.Infof("mkdir success!\n")
		}
	}
	return nil
}

// 判断文件夹是否存在
func pathExists(path string) (bool, error) {
	_, err := os.Stat(path)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}

func walkFunc(files string, info os.FileInfo, err error) error {
	_, fileName := filepath.Split(files)
	if path.Ext(files) == ".xlsx" && !strings.HasPrefix(fileName, "~$") && !strings.HasPrefix(fileName, "#") {
		fileCount++
		go parseXlsx(files, strings.Replace(fileName, ".xlsx", "", -1))
	}
	return nil
}

// 解析xlsx
func parseXlsx(path string, fileName string) {
	// 打开excel
	xlsx, err := excelize.OpenFile(path)
	if err != nil {
		log.Errorf("%s %s", fileName, err)
		ch <- ""
		return
	}

	sheetName := xlsx.GetSheetName(1)
	var lines = xlsx.GetRows(sheetName)

	fields := make([]string, 0)
	strlist := lines[config.FieldLine-1]
	for _, field := range strlist {
		if field != "" {
			fields = append(fields, field)
		}
	}

	fieldCount := len(fields)

	types := lines[config.TypeLine-1]

	var data []interface{}
	data = append(data, fields)

	var buffer bytes.Buffer

	lineNum := 0
	totalLineNum := len(lines)
	for n, line := range lines {
		line = line[0:fieldCount]
		if strings.HasPrefix(line[0], config.Comment) { // 注释符跳过
			continue
		}

		if line[0] == "" {
			log.Errorf("%s.xlsx (row=%v,col=0) error: is '' \n", fileName, n+1)
			continue
		}

		fieldNum := 0
		for _, value := range line {
			fieldNum++
			buffer.WriteString(value)
			if fieldNum < fieldCount {
				buffer.WriteString(config.Comma)
			}
		}
		if lineNum < totalLineNum {
			buffer.WriteString(config.Linefeed)
		}

		lineNum++
		if lineNum < config.DataLine {
			continue
		}

		var lineData []interface{}
		for i, value := range line {
			lineData = append(lineData, typeConvert(types[i], value))
		}
		data = append(data, lineData)
	}

	if !config.UseSheetName {
		sheetName = fileName
	}

	if config.Txt != "" {
		writeTxt(config.Txt, sheetName, &buffer)
	}

	if config.JSON != "" {
		writeJSON(config.JSON, sheetName, &data)
	}

	if config.Lua != "" {
		writeLuaTable(config.Lua, sheetName, &data)
	}

	ch <- sheetName
}

// 类型转换
func typeConvert(ty string, value string) interface{} {
	switch ty {
	case "int":
		arrValue := strings.Split(value, ".")
		if i, err := strconv.ParseInt(arrValue[0], 10, 64); err == nil {
			return i
		}
		return value
	case "float":
		if f, err := strconv.ParseFloat(value, 64); err == nil {
			return f
		}
		return value
	case "string":
		return value
	case "auto":
		m := make(map[string]interface{})
		if err := json.Unmarshal([]byte(value), &m); err == nil {
			return m
		}

		var arr []interface{}
		if err := json.Unmarshal([]byte(value), &arr); err == nil {
			return arr
		} else {
			if f, err := strconv.ParseFloat(value, 64); err == nil {
				return f
			}
		}
	}

	return value
}

// 转字为符串
func data2Str(data interface{}) string {
	b, err := json.Marshal(data)
	if err != nil {
		log.Errorln(err)
		return ""
	}
	return string(b)
}

// 写txt文件
func writeTxt(path string, fileName string, buffer *bytes.Buffer) {
	file, err := os.OpenFile(path+"/"+fileName+".txt", os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0666)
	if err != nil {
		log.Errorln("open file failed. ", err.Error())
		return
	}
	defer file.Close()
	file.Write(buffer.Bytes())
}

// 写JSON文件
func writeJSON(path string, fileName string, data interface{}) {
	file, err := os.OpenFile(path+"/"+fileName+".json", os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0666)
	if err != nil {
		log.Errorln("open file failed.", err.Error())
		return
	}

	defer file.Close()
	file.WriteString(data2Str(data))
}

// 写Lua文件
func writeLuaTable(path string, fileName string, data interface{}) {
	file, err := os.OpenFile(path+"/"+fileName+".lua", os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0666)
	if err != nil {
		log.Errorln("open file failed.", err.Error())
		return
	}

	defer file.Close()
	file.WriteString("return ")
	writeLuaTableContent(file, data, 0)
}

// 写Lua表内容
func writeLuaTableContent(file *os.File, data interface{}, idx int) {
	// 如果是指针类型
	if reflect.ValueOf(data).Type().Kind() == reflect.Pointer {
		data = reflect.ValueOf(data).Elem().Interface()
	}

	switch t := data.(type) {
	case int64:
		file.WriteString(fmt.Sprintf("%d", data))
	case float64:
		file.WriteString(fmt.Sprintf("%v", data))
	case string:
		file.WriteString(fmt.Sprintf(`"%s"`, data))
	case []interface{}:
		file.WriteString("{\n")
		a := data.([]interface{})
		for _, v := range a {
			addTabs(file, idx)
			writeLuaTableContent(file, v, idx+1)
			file.WriteString(",\n")
		}
		addTabs(file, idx-1)
		file.WriteString("}")
	case []string:
		file.WriteString("{\n")
		a := data.([]string)
		sort.Strings(a)
		for _, v := range a {
			addTabs(file, idx)
			writeLuaTableContent(file, v, idx+1)
			file.WriteString(",\n")
		}
		addTabs(file, idx-1)
		file.WriteString("}")
	case map[string]interface{}:
		m := data.(map[string]interface{})
		keys := make([]string, 0)
		for k := range m {
			keys = append(keys, k)
		}
		sort.Strings(keys)

		file.WriteString("{\n")
		for _, k := range keys {
			addTabs(file, idx)
			file.WriteString("[")
			writeLuaTableContent(file, k, idx+1)
			file.WriteString("] = ")
			writeLuaTableContent(file, m[k], idx+1)
			file.WriteString(",\n")
		}
		addTabs(file, idx-1)
		file.WriteString("}")
	default:
		file.WriteString(fmt.Sprintf("%t", data))
		_ = t
	}
}

// 在文件中添加制表符
func addTabs(file *os.File, idx int) {
	for i := 0; i < idx; i++ {
		file.WriteString("\t")
	}
}
